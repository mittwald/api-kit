package auth_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/mittwald/api-kit/auth"
)

// templated is a definition whose requests use every kind of template.
const templated = `
environments:
  production:
    apis:
      main: https://api.example.test/v1
      accounts: https://accounts.example.test/
defaults: {api: main}
auth:
  header: {name: X-Token, value: "Token ${token}"}
  validate:
    api: accounts
    method: get
    path: /sessions/${token}/check?verbose=1
    query: {scope: read write, holder: "$token&${token}"}
    valid: [200]
  refresh:
    path: sessions
    body:
      grant: refresh
      "${token}": {refresh: "${refreshToken}", tries: 3, strict: true, note: null}
      list: ["${refreshToken}", 1, ["$$${token} $other"]]
`

// TestRequestsAreBuiltFromTheDefinition covers the API, the method, the
// path, the query and the body of a request, and how values get into them.
func TestRequestsAreBuiltFromTheDefinition(t *testing.T) {
	f := newFixtureOf(t, templated)
	f.store.put(record("a/b c-token", `r"1-token`, ""))
	const checkRoute = "GET /sessions/a/b c-token/check"
	f.api.on(checkRoute, func(call) reply { return reply{status: 401} })
	f.api.on("POST /v1/sessions", func(call) reply { return reply{status: 200, body: rotated} })

	token, _, err := f.manager().Resolve(context.Background(), false)
	if err != nil || token != "rotated-token" {
		t.Fatalf("Resolve() = %q, %v", token, err)
	}
	// A value is one segment of a path and one value of a query, whatever
	// it holds; a request without a body has none.
	check := call{
		Route:  checkRoute,
		URL:    "https://accounts.example.test/sessions/a%2Fb%20c-token/check?verbose=1&scope=read+write&holder=a%2Fb+c-token%26a%2Fb+c-token",
		Header: "Token a/b c-token",
	}
	if got := f.api.sent(checkRoute); len(got) != 1 || got[0] != check {
		t.Errorf("validation requests = %+v, want %+v", got, check)
	}
	// A body keeps its shape; keys and strings are templates, and a
	// placeholder without a value stays as it is written.
	refresh := call{
		Route:  "POST /v1/sessions",
		URL:    "https://api.example.test/v1/sessions",
		Header: "Token a/b c-token",
		Body:   `{"grant":"refresh","a/b c-token":{"refresh":"r\"1-token","tries":3,"strict":true,"note":null},"list":["r\"1-token",1,["$a/b c-token $other"]]}`,
	}
	if got := f.api.sent("POST /v1/sessions"); len(got) != 1 || got[0] != refresh {
		t.Errorf("refresh requests = %+v, want %+v", got, refresh)
	}
}

// failingBody is a response that breaks off.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (failingBody) Close() error             { return nil }

// brokenAPI answers every request with a response that cannot be read.
type brokenAPI struct{}

func (brokenAPI) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: failingBody{}}, nil
}

func TestRequestThatFailsProvesNothing(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fixture)
		want   string
	}{
		{"an API the environment does not have", func(f *fixture) { f.options.Config.Validate.Request.API = "other" }, `no API \"other\"`},
		{"a method that is none", func(f *fixture) { f.options.Config.Validate.Request.Method = "NOT A METHOD" }, "invalid method"},
		{"a response that breaks off", func(f *fixture) { f.options.HTTP = brokenAPI{} }, "connection reset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.put("stored-token")
			tt.change(f)
			_, _, err := f.manager().Resolve(context.Background(), false)
			if !errors.Is(err, auth.ErrInconclusive) {
				t.Errorf("Resolve() error = %v, want that the validation is unavailable", err)
			}
			if !strings.Contains(f.log.String(), "level=WARN") || !strings.Contains(f.log.String(), tt.want) {
				t.Errorf("the log does not warn with %q:\n%s", tt.want, f.log)
			}
		})
	}
}
