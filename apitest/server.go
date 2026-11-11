package apitest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	apikit "github.com/mittwald/api-kit"
)

// baseURL is what [Server.URL] returns.
const baseURL = "https://api.example.test"

// Server is a fake API: it answers requests from a script and records them.
// It opens no socket. The client [Server.Client] returns hands every
// request to the Server, whatever host the request names.
//
// A Server is safe for concurrent use.
type Server struct {
	t testing.TB

	mu       sync.Mutex
	routes   map[string]*route
	requests []Request
}

// route is the script for one method and URL.
type route struct {
	steps []step
	hits  int
}

// step is one scripted answer, encoded and ready to send.
type step struct {
	status int
	header http.Header
	body   []byte
	err    error
	until  <-chan struct{}
}

// Response is the scripted answer to a request.
type Response struct {
	// Status is the HTTP status. Zero means 200.
	Status int
	// Body is sent as JSON. Nil sends no body, unless Raw is set.
	Body any
	// Raw is a body sent as it is, for answers that are not JSON, such as
	// an HTML error page. It is used when Body is nil.
	Raw string
	// Header are the response headers.
	Header http.Header
	// Err, when not nil, makes the request fail with it in place of an
	// answer, the way a connection that cannot be established does.
	Err error
	// Until, when not nil, holds the answer back until the channel is
	// closed. A request that is canceled while it waits fails with the
	// error of its context.
	Until <-chan struct{}
	// Sequence answers successive requests one after the other; its last
	// entry answers every further request. When it is not empty, the other
	// fields are ignored, as is the Sequence of an entry.
	Sequence []Response
}

// Request is a request a [Server] received.
type Request struct {
	// Method is the HTTP method.
	Method string
	// Host is the host the request was sent to, with the port if the URL
	// names one.
	Host string
	// Path is the path of the URL as it was sent, still escaped.
	Path string
	// Query is the query of the URL as it was sent, without the question
	// mark.
	Query string
	// Header are the request headers.
	Header http.Header
	// Body is the decoded JSON body, a value of the types [apikit.Decode]
	// produces, and nil for a request without a body. A body that is not
	// JSON is kept as a string.
	Body any
}

// New returns a Server without a script. A request it has no answer for
// fails t.
func New(t testing.TB) *Server {
	return &Server{t: t, routes: map[string]*route{}}
}

// Handle scripts the answer to the requests with the given method and URL.
// pathAndQuery is the path of the URL with its query, as in
// "/v1/pets?species=cat&limit=100", and answers requests to any host. With
// a scheme and a host before the path, as in
// "https://admin.example.test/v1/pets", it answers requests to that host
// only and takes precedence.
//
// The URL has to match exactly, but for the order and the escaping of the
// query parameters. Scripting the same method and URL again replaces the
// answer.
func (s *Server) Handle(method, pathAndQuery string, response Response) {
	s.t.Helper()
	target, err := url.Parse(pathAndQuery)
	if err != nil {
		s.t.Fatalf("apitest: cannot script %s %s: %v", method, pathAndQuery, err)
	}
	responses := response.Sequence
	if len(responses) == 0 {
		responses = []Response{response}
	}
	script := &route{}
	for _, entry := range responses {
		answer := step{status: entry.Status, header: entry.Header.Clone(), err: entry.Err, until: entry.Until}
		if answer.status == 0 {
			answer.status = http.StatusOK
		}
		if answer.header == nil {
			answer.header = http.Header{}
		}
		answer.body = []byte(entry.Raw)
		if entry.Body != nil {
			if answer.body, err = apikit.Marshal(entry.Body); err != nil {
				s.t.Fatalf("apitest: cannot encode the body scripted for %s %s: %v", method, pathAndQuery, err)
			}
			if answer.header.Get("Content-Type") == "" {
				answer.header.Set("Content-Type", "application/json")
			}
		}
		script.steps = append(script.steps, answer)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[routeKey(method, target, target.Host != "")] = script
}

// Client returns an HTTP client whose requests the Server answers.
func (s *Server) Client() *http.Client {
	return &http.Client{Transport: transport{s}}
}

// URL returns a base URL to put into a definition that has none of its
// own. The Server answers requests to every host, so any other URL works
// as well.
func (s *Server) URL() string {
	return baseURL
}

// Requests returns the requests received so far, in order of arrival.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// transport hands the requests of a client to the Server.
type transport struct {
	s *Server
}

// RoundTrip records the request and answers it from the script.
func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	s := t.s
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	received := Request{
		Method: req.Method,
		Host:   req.URL.Host,
		Path:   req.URL.EscapedPath(),
		Query:  req.URL.RawQuery,
		Header: req.Header.Clone(),
	}
	if len(body) > 0 {
		decoded, err := apikit.Unmarshal(body)
		if err != nil {
			decoded = string(body)
		}
		received.Body = decoded
	}

	s.mu.Lock()
	s.requests = append(s.requests, received)
	script, ok := s.routes[routeKey(req.Method, req.URL, true)]
	if !ok {
		script, ok = s.routes[routeKey(req.Method, req.URL, false)]
	}
	var answer step
	if ok {
		answer = script.steps[min(script.hits, len(script.steps)-1)]
		script.hits++
	}
	s.mu.Unlock()

	if !ok {
		message := fmt.Sprintf("apitest: no response scripted for %s %s", req.Method, req.URL)
		s.t.Errorf("%s", message)
		answer = step{status: http.StatusNotImplemented, header: http.Header{}, body: []byte(message)}
	}
	if answer.until != nil {
		select {
		case <-answer.until:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if answer.err != nil {
		return nil, answer.err
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", answer.status, http.StatusText(answer.status)),
		StatusCode:    answer.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        answer.header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(answer.body)),
		ContentLength: int64(len(answer.body)),
		Request:       req,
	}, nil
}

// routeKey returns what a script is filed under: the method and the URL in
// a canonical form, with or without its scheme and host.
func routeKey(method string, target *url.URL, withHost bool) string {
	var b strings.Builder
	b.WriteString(strings.ToUpper(method))
	b.WriteByte(' ')
	if withHost {
		b.WriteString(strings.ToLower(target.Scheme + "://" + target.Host))
	}
	b.WriteString(target.EscapedPath())
	if query := canonicalQuery(target.RawQuery); query != "" {
		b.WriteByte('?')
		b.WriteString(query)
	}
	return b.String()
}

// canonicalQuery returns a query with its parameters sorted by key, then
// value, and escaped in one way, so that two spellings of the same query
// compare equal.
func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	var pairs []string
	for pair := range strings.SplitSeq(raw, "&") {
		key, value, _ := strings.Cut(pair, "=")
		// The NUL byte sorts before every other one, so pairs sort by key
		// first.
		pairs = append(pairs, unescape(key)+"\x00"+unescape(value))
	}
	slices.Sort(pairs)
	for i, pair := range pairs {
		key, value, _ := strings.Cut(pair, "\x00")
		pairs[i] = url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	return strings.Join(pairs, "&")
}

// unescape decodes a query component. One that is not validly escaped is
// taken as it is.
func unescape(component string) string {
	decoded, err := url.QueryUnescape(component)
	if err != nil {
		return component
	}
	return decoded
}
