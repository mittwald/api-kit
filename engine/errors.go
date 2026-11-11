package engine

import (
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// HTTPError is an answer of the API that is no success. The engine returns
// it wrapped in an [*apikit.Error], whose kind is [apikit.KindNotFound] for
// status 404 in answer to a GET and [apikit.KindFailed] otherwise, and
// whose message says what failed in terms of the resource.
type HTTPError struct {
	// Method is the HTTP method of the request.
	Method string
	// URL is the URL of the request.
	URL string
	// Status is the HTTP status of the answer.
	Status int
	// Body is the body of the answer as it is shown to a person: a JSON
	// object as it came, anything else without markup and blank lines.
	Body string
	// Resource is the resource the request was about.
	Resource string
	// ID is the ID of the item the request was about, if it was about one.
	ID string
}

// Error returns the request, the status and the body.
func (e *HTTPError) Error() string {
	message := fmt.Sprintf("%s %s: %d", e.Method, e.URL, e.Status)
	if e.Body != "" {
		message += " " + e.Body
	}
	return message
}

// httpError returns the error for an answer to c that is no success.
func (e *Engine) httpError(c call, got answer) error {
	kind := apikit.KindFailed
	if got.status == http.StatusNotFound && c.method == http.MethodGet {
		kind = apikit.KindNotFound
	}
	what := c.resource
	if c.id != "" {
		what = e.doc.Resource(c.resource).Singular + " by ID " + c.id
	}
	message := fmt.Sprintf("failed to %s %s: %d", verbOf(c), what, got.status)
	if got.body != "" {
		message += " " + got.body
	}
	return &apikit.Error{Kind: kind, Msg: message, Err: &HTTPError{
		Method:   c.method,
		URL:      c.url,
		Status:   got.status,
		Body:     got.body,
		Resource: c.resource,
		ID:       c.id,
	}}
}

// notFound returns the error for a value that names no item of a resource.
func (e *Engine) notFound(resource, value string) error {
	return apikit.Errorf(apikit.KindNotFound, "unable to find %s %s", e.doc.Resource(resource).Singular, value)
}

var (
	// hiddenMarkup matches the parts of an HTML document that hold no
	// text for a reader: scripts, style sheets and comments.
	hiddenMarkup = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)\s*>|<!--.*?-->`)
	// markupTag matches a tag, a declaration or a processing instruction.
	markupTag = regexp.MustCompile(`(?s)</?[a-zA-Z!?][^>]*>`)
)

// cleanBody returns the body of a failed request as it is shown to a
// person. A JSON object is kept as it is. Anything else is taken for an
// HTML page, as proxies and gateways answer with: the markup goes, the
// character references are resolved, and of the lines those remain that
// say something, which the word "error" alone does not.
func cleanBody(body string) string {
	if strings.HasPrefix(body, "{") {
		return body
	}
	text := markupTag.ReplaceAllString(hiddenMarkup.ReplaceAllString(body, ""), "")
	var lines []string
	for line := range strings.SplitSeq(html.UnescapeString(text), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.EqualFold(line, "error") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
