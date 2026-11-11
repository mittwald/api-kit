package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	apikit "github.com/mittwald/api-kit"
)

// call describes one request to the API.
type call struct {
	// method is the HTTP method, in upper case.
	method string
	// url is where the request goes.
	url string
	// header is sent besides the credential. It may be nil.
	header http.Header
	// body is sent as JSON. Nil sends no body.
	body any
	// token is sent in place of the credential of the Engine. A request
	// with a token of its own is never answered from memory, and when the
	// API rejects the token, the request fails.
	token string
	// verb says what the request does to the resource, for messages. Empty
	// means "fetch".
	verb string
	// resource and id name what the request is about, for messages.
	resource, id string
}

// reply is a response as it came off the wire.
type reply struct {
	status int
	body   []byte
}

// answer is what a request came to: the status, with the decoded value of
// a success or the body of a failure, cleaned up for display.
type answer struct {
	status int
	value  any
	body   string
}

// ok reports whether the answer is a success.
func (a answer) ok() bool {
	return a.status >= 200 && a.status < 300
}

// flight is a GET under way that other callers wait for.
type flight struct {
	// done is closed when answer and err are set.
	done   chan struct{}
	answer answer
	err    error
	// abandoned is set when the request failed because its own caller gave
	// up. Whoever else waits for it then has to ask for itself.
	abandoned bool
}

// request sends c and returns the decoded body of the answer: nil for an
// answer without one. An answer that is no success is an error that wraps
// an [*HTTPError].
//
// The answers to GETs are remembered by URL, and GETs for one URL that are
// under way at the same time share one request. The caller owns the value
// it gets: it is a copy of what is remembered.
func (e *Engine) request(ctx context.Context, c call) (any, error) {
	var got answer
	var err error
	if c.method == http.MethodGet && c.token == "" {
		got, err = e.remembered(ctx, c)
	} else {
		// Requests that change something must reach the API every time,
		// and what they answer says nothing about a later GET. A token of
		// the request's own may see what the Engine's credential does not.
		got, err = e.exchange(ctx, c)
	}
	if err != nil {
		return nil, err
	}
	if !got.ok() {
		return nil, e.httpError(c, got)
	}
	return apikit.Clone(got.value), nil
}

// remembered answers a GET from memory, from a request for the same URL
// that is under way, or by sending it.
func (e *Engine) remembered(ctx context.Context, c call) (answer, error) {
	for {
		e.mu.Lock()
		if kept, ok := e.answers[c.url]; ok {
			e.mu.Unlock()
			return kept, nil
		}
		under, ok := e.inflight[c.url]
		if !ok {
			under = &flight{done: make(chan struct{})}
			e.inflight[c.url] = under
			e.mu.Unlock()
			e.fly(ctx, c, under)
			return under.answer, under.err
		}
		e.mu.Unlock()

		select {
		case <-under.done:
		case <-ctx.Done():
			return answer{}, ctx.Err()
		}
		if !under.abandoned {
			return under.answer, under.err
		}
	}
}

// fly sends the GET that others may be waiting for and files its answer.
func (e *Engine) fly(ctx context.Context, c call, under *flight) {
	defer func() {
		e.mu.Lock()
		if under.err == nil && keeps(under.answer.status) {
			e.answers[c.url] = under.answer
		}
		delete(e.inflight, c.url)
		e.mu.Unlock()
		close(under.done)
	}()
	// Should the exchange panic, the waiting callers still get an error.
	under.err = errors.New("the request was not completed")
	got, err := e.exchange(ctx, c)
	under.answer, under.err = got, err
	under.abandoned = err != nil && ctx.Err() != nil
}

// keeps reports whether an answer with this status is remembered: a
// success, and a resource that does not exist. Any other failure may pass.
func keeps(status int) bool {
	return status == http.StatusOK || status == http.StatusNoContent || status == http.StatusNotFound
}

// exchange sends c with a credential and returns the answer. When the API
// rejects the Engine's credential, the token source is asked for another
// one and the request is sent once more. What the token source fails with
// is returned as it is: it knows best why there is no token.
func (e *Engine) exchange(ctx context.Context, c call) (answer, error) {
	var payload []byte
	if c.body != nil {
		var err error
		if payload, err = apikit.Marshal(c.body); err != nil {
			return answer{}, e.failed(c, fmt.Errorf("the request body cannot be encoded: %w", err))
		}
	}

	// own is set when the request carries the Engine's credential and not
	// a token of its own.
	token, own := c.token, c.token == "" && e.tokens != nil
	if own {
		var err error
		if token, err = e.tokens.Token(ctx); err != nil {
			return answer{}, err
		}
		if token == "" {
			return answer{}, apikit.Errorf(apikit.KindUnauthenticated, "no API token available")
		}
	}

	got, err := e.attempts(ctx, c, payload, token)
	if err == nil && got.status == http.StatusUnauthorized && own {
		replacement, rejected := e.tokens.Rejected(ctx, token)
		if rejected != nil {
			return answer{}, rejected
		}
		if replacement != "" && replacement != token {
			token = replacement
			// The new token gets every attempt the old one had.
			got, err = e.attempts(ctx, c, payload, token)
		}
	}
	if err != nil {
		return answer{}, err
	}
	if got.status == http.StatusUnauthorized && token != "" {
		if !own {
			return answer{}, apikit.Errorf(apikit.KindUnauthenticated, "the token of the steps was rejected by the server")
		}
		return answer{}, apikit.Errorf(apikit.KindUnauthenticated, "API token rejected by the server, please re-authenticate")
	}

	result := answer{status: got.status}
	switch {
	case !result.ok():
		result.body = cleanBody(string(got.body))
	case got.status != http.StatusNoContent && len(bytes.TrimSpace(got.body)) > 0:
		if result.value, err = apikit.Unmarshal(got.body); err != nil {
			return answer{}, e.failed(c, fmt.Errorf("%s %s: the response is not JSON: %w", c.method, c.url, err))
		}
	}
	if own && result.ok() {
		e.tokens.Confirmed(token)
	}
	return result, nil
}

// attempts sends c and returns the response. A GET is sent again after a
// response with one of the statuses the definition names and after a
// request that got no response at all, for a timeout or a lost connection,
// with a pause that doubles each time. Any other request is sent once: it
// may have reached the API.
func (e *Engine) attempts(ctx context.Context, c call, payload []byte, token string) (reply, error) {
	tries := 1
	if c.method == http.MethodGet {
		tries += e.doc.HTTP.Retries
	}
	pause := seconds(e.doc.HTTP.RetryDelaySeconds)
	for try := 1; ; try++ {
		req, err := e.newRequest(ctx, c, payload, token)
		if err != nil {
			// Sending it again would not build it either.
			return reply{}, e.failed(c, err)
		}
		got, err := e.send(req, c, try)
		switch {
		case err != nil && ctx.Err() != nil:
			return reply{}, ctx.Err()
		case err != nil && (try == tries || errors.Is(err, errRedirects)):
			return reply{}, e.failed(c, fmt.Errorf("%s %s: %w", c.method, c.url, err))
		case err == nil && (try == tries || !slices.Contains(e.doc.HTTP.RetryStatuses, got.status)):
			return got, nil
		}
		if err := e.sleep(ctx, pause); err != nil {
			return reply{}, err
		}
		pause *= 2
	}
}

// newRequest builds the HTTP request for c, with its headers, its body and
// the credential.
func (e *Engine) newRequest(ctx context.Context, c call, payload []byte, token string) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, c.method, c.url, body)
	if err != nil {
		return nil, err
	}
	for name, values := range c.header {
		req.Header[name] = slices.Clone(values)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req = e.withCredential(req, token)
	}
	return req, nil
}

// send sends req, reads the whole response and logs the attempt. An error
// means that no complete response arrived.
//
// Neither the error nor the log line names the URL as it was sent, which
// may hold the credential: both go with the URL of c.
func (e *Engine) send(req *http.Request, c call, try int) (reply, error) {
	start := time.Now()
	got, err := e.do(req)
	attrs := []slog.Attr{slog.String("method", c.method), slog.String("url", c.url), slog.Int("attempt", try)}
	if err != nil {
		if failure, ok := errors.AsType[*url.Error](err); ok {
			err = failure.Err
		}
		attrs = append(attrs, slog.String("error", err.Error()))
	} else {
		attrs = append(attrs, slog.Int("status", got.status))
	}
	attrs = append(attrs, slog.Duration("duration", time.Since(start)))
	e.log.LogAttrs(req.Context(), slog.LevelDebug, "request", attrs...)
	return got, err
}

// do sends req and reads the whole response.
func (e *Engine) do(req *http.Request) (reply, error) {
	resp, err := e.http.Do(req)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return reply{}, err
	}
	return reply{status: resp.StatusCode, body: body}, nil
}

// failed returns the error for a request that got no usable answer.
func (e *Engine) failed(c call, err error) error {
	return apikit.Errorf(apikit.KindFailed, "API request failed while trying to %s %s: %w", verbOf(c), c.resource, err)
}

// verbOf says what a request does to its resource.
func verbOf(c call) string {
	if c.verb == "" {
		return "fetch"
	}
	return c.verb
}
