package cli_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
)

// secured gives the pet shop authentication: a header of its own, a token
// from the store or the environment, a request that checks a token, and two
// ways to log in.
const secured = `
auth:
  header: {name: X-Token, value: "${token}"}
  sources:
    - store
    - {env: SHOP_TOKEN}
  validate: {api: main, method: POST, path: tokens/check}
  login:
    fallback: password
    methods:
      password:
        kind: password
        prompts:
          - {name: username, label: Username}
          - {name: password, label: Password, secret: true}
        request: {api: main, method: POST, path: login, body: {user: "${username}", pass: "${password}"}}
      sso:
        kind: browser
        url: {api: main, path: sso/start, query: {back: "${callbackUrl}"}}
`

// answers is a user who answers every question from a list and has no
// browser.
type answers map[string]string

func (a answers) Interactive() bool { return true }

func (a answers) Ask(_ context.Context, p definition.Prompt) (string, error) {
	return a[p.Name], nil
}

func (a answers) OpenBrowser(context.Context, string) error { return errors.New("no browser here") }

func (a answers) Notify(string) {}

// securedTool returns a tool for the pet shop with authentication.
func securedTool(t *testing.T) *tool {
	t.Helper()
	h := newTool(t)
	h.overlay(secured)
	h.options.UI = answers{"username": "ada", "password": "s3cret"}
	return h
}

func TestAuthStatus(t *testing.T) {
	h := securedTool(t)
	h.api.Handle("POST", mainAPI+"/tokens/check", apitest.Response{Status: 204})

	stdout, stderr, code := h.run("auth", "status")
	if code != apikit.ExitOK || stdout != "Using API token from --token\n" || stderr != "" {
		t.Errorf("with --token: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if sent := h.api.Requests(); len(sent) != 1 || sent[0].Header.Get("X-Token") != "secret" {
		t.Errorf("the token was not put to the API in its header: %+v", sent)
	}

	h.env["SHOP_TOKEN"] = "from-env"
	stdout, _, code = h.runBare("auth", "status")
	if code != apikit.ExitOK || stdout != "Using API token from the environment variable SHOP_TOKEN\n" {
		t.Errorf("with a token in the environment: exit code = %d, stdout = %q", code, stdout)
	}
}

func TestAuthStatusWithoutAToken(t *testing.T) {
	h := securedTool(t)
	stdout, stderr, code := h.runBare("auth", "status")
	// Not being logged in is an answer, not a failure.
	if code != apikit.ExitOK || stdout != "Not authenticated\n" || stderr != "" {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if sent := h.api.Requests(); len(sent) != 0 {
		t.Errorf("%d requests were sent without a token", len(sent))
	}
}

func TestAuthStatusWhenTheAPIGivesNoVerdict(t *testing.T) {
	h := securedTool(t)
	h.api.Handle("POST", mainAPI+"/tokens/check", apitest.Response{Status: 503})
	stdout, stderr, code := h.run("auth", "status")
	if code != apikit.ExitFailed || stdout != "" || !strings.HasPrefix(stderr, "ERROR: ") || !strings.HasSuffix(stderr, "Try again once the API is reachable.\n") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestAuthLoginStoresASessionThatLaterRunsUse(t *testing.T) {
	h := securedTool(t)
	h.api.Handle("POST", mainAPI+"/login", apitest.Response{Body: raw(`{"token": "fresh", "refreshToken": "again", "expiresAt": "2099-01-01T00:00:00Z"}`)})
	h.api.Handle("POST", mainAPI+"/tokens/check", apitest.Response{Status: 204})
	h.api.Handle("GET", adminAPI+"/pets?limit=100", apitest.Response{Body: raw(`[{"id": "0a1b2c3d"}]`)})

	stdout, stderr, code := h.runBare("auth", "login")
	if code != apikit.ExitOK || stdout != "Stored the session for production\n" {
		t.Fatalf("login: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	sent := h.api.Requests()
	if len(sent) != 2 || sent[0].Path != "/v1/login" || sent[1].Header.Get("X-Token") != "fresh" {
		t.Fatalf("requests of the login: %+v", sent)
	}
	if got := apikit.Text(sent[0].Body); got != `{"user": "ada", "pass": "s3cret"}` {
		t.Errorf("the login sent %s, want the answers to the prompts", got)
	}

	// The stored session authenticates the next run.
	if stdout, stderr, code = h.runBare("get", "pets", "-o", "id"); code != apikit.ExitOK || stdout != "0a1b2c3d\n" {
		t.Fatalf("get after the login: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if last := h.api.Requests(); last[len(last)-1].Header.Get("X-Token") != "fresh" {
		t.Errorf("the request after the login does not carry the session's token")
	}
	if stdout, _, _ = h.runBare("auth", "status"); stdout != "Using API token from the stored session for production\n" {
		t.Errorf("status after the login: %q", stdout)
	}

	// Each environment has a session of its own.
	if stdout, _, _ = h.runBare("auth", "status", "--env", "staging"); stdout != "Not authenticated\n" {
		t.Errorf("status of another environment: %q", stdout)
	}

	if stdout, _, code = h.runBare("auth", "reset"); code != apikit.ExitOK || stdout != "Removed the stored session for production\n" {
		t.Errorf("reset: exit code = %d, stdout = %q", code, stdout)
	}
	if stdout, _, code = h.runBare("auth", "reset"); code != apikit.ExitOK || stdout != "No session is stored for production\n" {
		t.Errorf("reset again: exit code = %d, stdout = %q", code, stdout)
	}
	if stdout, _, _ = h.runBare("auth", "status"); stdout != "Not authenticated\n" {
		t.Errorf("status after the reset: %q", stdout)
	}
}

func TestAuthLoginByName(t *testing.T) {
	h := securedTool(t)
	h.api.Handle("POST", mainAPI+"/login", apitest.Response{Status: 403, Body: raw(`{"message": "wrong password"}`)})
	stdout, stderr, code := h.runBare("auth", "login-password")
	if code != apikit.ExitFailed || stdout != "" || !strings.Contains(stderr, "wrong password") {
		t.Errorf("a refused login: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "s3cret") {
		t.Errorf("the error shows the password:\n%s", stderr)
	}

	// The browser login starts, and fails here for want of a browser that
	// comes back.
	h.env["SHOP_HTTP_TIMEOUT"] = "1"
	_, stderr, code = h.runBare("auth", "login-sso", "--help")
	if code != apikit.ExitOK || stderr != "" {
		t.Errorf("help of the browser login: exit code = %d, stderr = %q", code, stderr)
	}
}

func TestAuthCommands(t *testing.T) {
	h := securedTool(t)
	stdout, stderr, code := h.runBare("auth")
	if code != apikit.ExitFailed || stderr != "" {
		t.Errorf("auth alone: exit code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"status", "reset", "login ", "login-password", "login-sso"} {
		if !strings.Contains(stdout, "  "+want) {
			t.Errorf("the help of auth does not list %q:\n%s", want, stdout)
		}
	}

	// A definition without logins has no login command.
	bareTool := newTool(t)
	stdout, _, _ = bareTool.runBare("auth", "--help")
	if strings.Contains(stdout, "login") || !strings.Contains(stdout, "status") {
		t.Errorf("auth of a definition without logins:\n%s", stdout)
	}
}

func TestMissingTokenHintsAtTheLogin(t *testing.T) {
	h := securedTool(t)
	// Nobody is at the terminal, so nothing is asked.
	h.options.UI = nil
	_, stderr, code := h.runBare("get", "pets")
	if code != apikit.ExitFailed || !strings.HasPrefix(stderr, "ERROR: ") || !strings.HasSuffix(stderr, "Run 'shop auth login' to log in.\n") {
		t.Errorf("exit code = %d, stderr = %q", code, stderr)
	}

	// Without a login in the definition there is nothing to hint at.
	_, stderr, _ = newTool(t).runBare("get", "pets")
	if strings.Contains(stderr, "auth login") {
		t.Errorf("a definition without logins hints at one: %q", stderr)
	}
}
