package auth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/definition"
)

// credentials are the answers to the prompts of the password login.
func credentials() map[string]string {
	return map[string]string{"Username": " ada@example.test ", "Password": "hunter-secret"}
}

func TestPasswordLogin(t *testing.T) {
	f := newFixture(t)
	f.ui.answers = credentials()
	// An answer is sent as it is, whatever it holds.
	f.ui.answers["Password"] = `p"ss\word-secret`
	f.api.on(loginRoute, func(call) reply {
		return reply{status: 200, body: `{"token": "left-token:right-token:rest", "refreshToken": "login-refresh-token", "expires": "2030-06-01T12:00:00+02:00"}`}
	})
	f.api.on(checkRoute, func(received call) reply {
		// The token is put to the API before it is stored.
		if stored := f.store.stored(); stored != "" {
			t.Errorf("stored %s before the validation", stored)
		}
		if received.Header != "left-token:right-token" {
			return reply{status: 401}
		}
		return reply{status: 204}
	})
	m := f.manager()

	// The password login asks on any run: its answers may come from a pipe.
	source, err := m.Login(context.Background(), "")
	if err != nil || source != (auth.Source{Kind: auth.SourceLogin, Name: "password"}) {
		t.Fatalf("Login() = %v, %v", source, err)
	}
	if want := []definition.Prompt{{Name: "username", Label: "Username"}, {Name: "password", Label: "Password", Secret: true}}; !slices.Equal(f.ui.asked, want) {
		t.Errorf("asked %+v, want %+v", f.ui.asked, want)
	}
	// The answers are sent as JSON, whatever they hold, and without a token.
	want := call{Route: loginRoute, URL: "https://api.example.test/v1/login", Body: `{"email":"ada@example.test","password":"p\"ss\\word-secret"}`}
	if got := f.api.sent(loginRoute); !slices.Equal(got, []call{want}) {
		t.Errorf("login requests = %+v, want %+v", got, want)
	}
	// Of the token only what the pattern of the definition matches counts.
	if got, want := f.store.stored(), record("left-token:right-token", "login-refresh-token", "2030-06-01T10:00:00Z"); got != want {
		t.Errorf("stored %s, want %s", got, want)
	}
	if len(f.ui.notices) != 0 {
		t.Errorf("notices = %q", f.ui.notices)
	}

	// The login has proven the token.
	before := len(f.api.calls)
	if token, err := m.Token(context.Background()); err != nil || token != "left-token:right-token" {
		t.Errorf("Token() = %q, %v", token, err)
	}
	if source, validated, err := m.Status(context.Background()); err != nil || !validated || source.Kind != auth.SourceProcess {
		t.Errorf("Status() = %v, %v, %v", source, validated, err)
	}
	if len(f.api.calls) != before {
		t.Errorf("the token of the login cost %d more requests", len(f.api.calls)-before)
	}
}

func TestPasswordLoginFailures(t *testing.T) {
	session := `{"token": "left-token:right-token"}`
	tests := []struct {
		name    string
		login   reply
		verdict int
		kind    apikit.Kind
		want    string
	}{
		{"the API refuses the credentials", reply{status: 401, body: "{\"message\":\n \"wrong password hunter-secret for ada\"}"}, 204,
			apikit.KindUnauthenticated, `login "password" failed: the API answered 401: {"message": "wrong password *** for ada"}`},
		{"the API refuses without a word", reply{status: 403}, 204,
			apikit.KindUnauthenticated, `login "password" failed: the API answered 403`},
		{"a long answer is cut between two characters", reply{status: 500, body: "x" + strings.Repeat("ä", 400)}, 204,
			apikit.KindUnauthenticated, "the API answered 500: x" + strings.Repeat("ä", 149) + "…"},
		{"the API does not answer", reply{err: &url.Error{Op: "Post", URL: "https://api.example.test/v1/login?key=url-secret", Err: errors.New("connection refused")}}, 204,
			apikit.KindFailed, `login "password" failed: POST login: connection refused`},
		{"the response holds no token", reply{status: 200, body: `{"refreshToken": "login-refresh-token"}`}, 204,
			apikit.KindUnauthenticated, "the response holds no token"},
		{"the response is no object", reply{status: 200, body: `["left-token:right-token"]`}, 204,
			apikit.KindUnauthenticated, "the response is not a JSON object"},
		{"the token does not match the pattern", reply{status: 200, body: `{"token": "plain-token"}`}, 204,
			apikit.KindUnauthenticated, "the value of token does not match ^[^:]+:[^:]+"},
		{"the API rejects the new token", reply{status: 200, body: session}, 401,
			apikit.KindUnauthenticated, `the API rejected the token of login "password"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.ui.answers = credentials()
			f.store.put("stored-token")
			f.api.on(loginRoute, func(call) reply { return tt.login })
			f.api.verdicts["left-token:right-token"] = tt.verdict

			source, err := f.manager().Login(context.Background(), "password")
			if err == nil || source != (auth.Source{}) {
				t.Fatalf("Login() = %v, %v; want an error", source, err)
			}
			if kind := apikit.KindOf(err); kind != tt.kind {
				t.Errorf("KindOf(%v) = %v, want %v", err, kind, tt.kind)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Login() error = %q, want it to contain %q", err, tt.want)
			}
			for _, mark := range []string{"-token", "-secret"} {
				if strings.Contains(err.Error(), mark) {
					t.Errorf("the error holds a secret: %v", err)
				}
			}
			// A login that fails leaves the stored session alone.
			if got := f.store.stored(); got != "stored-token" {
				t.Errorf("stored %q", got)
			}
		})
	}

	t.Run("the API repeats a secret the way JSON spells it", func(t *testing.T) {
		f := newFixture(t)
		f.ui.answers = credentials()
		f.ui.answers["Password"] = `hunt"er \ secret`
		f.api.on(loginRoute, func(received call) reply {
			return reply{status: 422, body: `{"rejected": ` + received.Body + `}`}
		})
		_, err := f.manager().Login(context.Background(), "password")
		want := `login "password" failed: the API answered 422: {"rejected": {"email":"ada@example.test","password":"***"}}`
		if err == nil || err.Error() != want {
			t.Errorf("Login() error = %v, want %s", err, want)
		}
	})

	t.Run("the user does not answer", func(t *testing.T) {
		f := newFixture(t)
		f.ui.abort = errors.New("interrupted")
		_, err := f.manager().Login(context.Background(), "password")
		if !errors.Is(err, f.ui.abort) || len(f.ui.asked) != 1 || len(f.api.calls) != 0 {
			t.Errorf("Login() = %v after %d prompts and %d requests", err, len(f.ui.asked), len(f.api.calls))
		}
	})
}

func TestLoginKeepsATokenWithoutAVerdict(t *testing.T) {
	f := newFixture(t)
	f.ui.answers = credentials()
	f.api.on(loginRoute, func(call) reply { return reply{status: 201, body: `{"token": "left-token:right-token"}`} })
	f.api.verdicts["left-token:right-token"] = http.StatusBadGateway
	m := f.manager()

	if _, err := m.Login(context.Background(), "password"); err != nil {
		t.Fatalf("Login(): %v", err)
	}
	if got := f.store.stored(); got != record("left-token:right-token", "", "") {
		t.Errorf("stored %s", got)
	}
	if len(f.ui.notices) != 2 || !strings.Contains(f.ui.notices[0], "validation is unavailable") || !strings.Contains(f.ui.notices[1], "no refresh token") {
		t.Errorf("notices = %q, want one about the validation and one about the refresh token", f.ui.notices)
	}
	// The token is used, and still has to prove itself.
	if token, err := m.Token(context.Background()); err != nil || token != "left-token:right-token" {
		t.Errorf("Token() = %q, %v", token, err)
	}
	if _, validated, err := m.Status(context.Background()); validated || !errors.Is(err, auth.ErrInconclusive) {
		t.Errorf("Status() = validated %v, %v", validated, err)
	}
}

func TestLoginThatCannotBeStoredFails(t *testing.T) {
	f := newFixture(t)
	f.ui.answers = credentials()
	f.store.broken = errors.New("the keyring is locked")
	f.api.on(loginRoute, func(call) reply {
		return reply{status: 200, body: `{"token": "left-token:right-token", "refreshToken": "login-refresh-token"}`}
	})
	f.api.valid("left-token:right-token")

	_, err := f.manager().Login(context.Background(), "password")
	if err == nil || !strings.Contains(err.Error(), "cannot store the session: the keyring is locked") {
		t.Errorf("Login() error = %v, want the error of the store", err)
	}
}

func TestLoginWithoutValidation(t *testing.T) {
	f := newFixture(t)
	f.options.Config.Validate = nil
	f.options.Config.Refresh = nil
	f.options.Explicit = "given-token"
	f.ui.answers = credentials()
	f.api.on(loginRoute, func(call) reply { return reply{status: 200, body: `{"token": "left-token:right-token"}`} })
	m := f.manager()

	if _, err := m.Login(context.Background(), "password"); err != nil {
		t.Fatalf("Login(): %v", err)
	}
	if got := f.store.stored(); got != record("left-token:right-token", "", "") || len(f.ui.notices) != 0 {
		t.Errorf("stored %s with the notices %q", got, f.ui.notices)
	}
	// The run keeps the token it was given.
	if token, err := m.Token(context.Background()); err != nil || token != "given-token" {
		t.Errorf("Token() = %q, %v", token, err)
	}
}

func TestLoginNeedsAMethodAndAUser(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fixture)
		method string
		kind   apikit.Kind
		want   string
	}{
		{"an unknown method", func(*fixture) {}, "token", apikit.KindUsage, `unknown login method "token"`},
		{"no fallback method", func(f *fixture) { f.options.Config.Login.Fallback = "" }, "", apikit.KindUsage, "names no login method to fall back to"},
		{"no user", func(f *fixture) { f.options.UI = nil }, "", apikit.KindUnauthenticated, `login "password" needs an interactive terminal`},
		{"a browser login without a terminal", func(*fixture) {}, "sso", apikit.KindUnauthenticated, `login "sso" needs an interactive terminal`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.change(f)
			_, err := f.manager().Login(context.Background(), tt.method)
			if err == nil || apikit.KindOf(err) != tt.kind || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Login() error = %v (%v), want %q of kind %v", err, apikit.KindOf(err), tt.want, tt.kind)
			}
			if len(f.ui.asked) != 0 || len(f.ui.opened) != 0 || len(f.api.calls) != 0 {
				t.Errorf("asked %v, opened %v, sent %v", f.ui.asked, f.ui.opened, f.api.calls)
			}
		})
	}
}

// visit requests a page as a browser does and returns the status and the
// body.
func visit(t *testing.T, method, address string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, address, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

// comeBack stands in for the browser and the login page of the API: it
// requests the address the login URL names for the way back, and returns
// that address through its argument.
func comeBack(t *testing.T, callback *string) func(context.Context, string) error {
	return func(_ context.Context, address string) error {
		login, err := url.Parse(address)
		if err != nil {
			t.Errorf("the login URL %q: %v", address, err)
			return nil
		}
		*callback = login.Query().Get("redirectUri")
		if status, _ := visit(t, http.MethodGet, *callback); status != http.StatusOK {
			t.Errorf("GET of the callback address answered %d", status)
		}
		return nil
	}
}

func TestBrowserLogin(t *testing.T) {
	f := newFixture(t)
	f.ui.interactive = true
	f.ui.answers["Paste the token"] = ` {"id": "s-1", "token": "pasted-token", "refreshToken": "pasted-refresh-token", "expires": "2030-03-01T00:00:00"} `
	f.api.valid("pasted-token")
	var callback, page string
	f.ui.browse = func(_ context.Context, address string) error {
		login, err := url.Parse(address)
		if err != nil {
			t.Fatalf("the login URL %q: %v", address, err)
		}
		callback = login.Query().Get("redirectUri")
		// Only the address that was handed out ends the wait.
		for _, other := range []string{callback + "x", strings.TrimSuffix(callback, callback[strings.LastIndex(callback, "/"):]), callback[:strings.Index(callback, "/callback")] + "/"} {
			if status, _ := visit(t, http.MethodGet, other); status != http.StatusNotFound {
				t.Errorf("GET %s answered %d, want 404", other, status)
			}
		}
		if status, _ := visit(t, http.MethodPost, callback); status != http.StatusNotFound {
			t.Errorf("POST to the callback address answered %d, want 404", status)
		}
		if len(f.ui.asked) != 0 {
			t.Error("the user was asked before the browser came back")
		}
		var status int
		if status, page = visit(t, http.MethodGet, callback+"?state=ignored"); status != http.StatusOK {
			t.Errorf("GET of the callback address answered %d", status)
		}
		return nil
	}

	source, err := f.manager().Login(context.Background(), "sso")
	if err != nil || source != (auth.Source{Kind: auth.SourceLogin, Name: "sso"}) {
		t.Fatalf("Login() = %v, %v", source, err)
	}

	// The login page gets the address to come back to: the loopback
	// interface, a port of the system's choice and a path nobody can guess.
	wantURL := regexp.MustCompile(`^https://api\.example\.test/v1/sso/start\?redirectUri=http%3A%2F%2F127\.0\.0\.1%3A[0-9]+%2Fcallback%2F[A-Z2-7]{26}$`)
	if len(f.ui.opened) != 1 || !wantURL.MatchString(f.ui.opened[0]) {
		t.Errorf("opened %q, want one URL like %s", f.ui.opened, wantURL)
	}
	if len(f.ui.notices) != 1 || !strings.HasPrefix(f.ui.notices[0], "Opening the login page in your browser.") || !strings.HasSuffix(f.ui.notices[0], "\n"+f.ui.opened[0]) {
		t.Errorf("notices = %q, want one that ends in the login URL", f.ui.notices)
	}

	// The page the browser lands on sends it on to the API with an empty
	// form, so that the browser shows the session.
	for _, part := range []string{
		"<title>petctl login</title>",
		`<form id="next" method="post" action="https://api.example.test/v1/tokens/show">`,
		`<script>document.getElementById("next").submit();</script>`,
		`<noscript><button type="submit">`,
	} {
		if !strings.Contains(page, part) {
			t.Errorf("the page lacks %s:\n%s", part, page)
		}
	}

	if want := []definition.Prompt{{Label: "Paste the token", Secret: true, Hint: "Copy the token from the browser response."}}; !slices.Equal(f.ui.asked, want) {
		t.Errorf("asked %+v, want %+v", f.ui.asked, want)
	}
	// An expiry without a time zone is in UTC.
	if got, want := f.store.stored(), record("pasted-token", "pasted-refresh-token", "2030-03-01T00:00:00Z"); got != want {
		t.Errorf("stored %s, want %s", got, want)
	}
	if got := f.api.tokens(checkRoute); !slices.Equal(got, []string{"pasted-token"}) {
		t.Errorf("validated %v", got)
	}

	// The wait is over, and so is the server.
	client := http.Client{Timeout: time.Second}
	if response, err := client.Get(callback); err == nil {
		_ = response.Body.Close()
		t.Errorf("the callback address still answers after the login: %d", response.StatusCode)
	}
}

func TestCallbackPage(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fixture)
		want   []string
		lacks  string
	}{
		{"the name of the tool is escaped", func(f *fixture) { f.options.AppName = `pet<ctl> & "co"` },
			[]string{"<title>pet&lt;ctl&gt; &amp; &#34;co&#34; login</title>"}, "<ctl>"},
		{"a tool without a name", func(f *fixture) { f.options.AppName = "" },
			[]string{"<title>Login</title>"}, ""},
		{"without a place to send the browser on to", func(f *fixture) { f.options.Config.Login.Methods[1].Submit = nil },
			[]string{"<p>Login complete. You can close this window and return to the terminal.</p>"}, "<form"},
		{"the form takes the method and the address of the definition", func(f *fixture) {
			f.options.Config.Login.Methods[1].Submit = &definition.AuthRequest{
				API: "main", Method: "GET", Path: "tokens/show",
				Query: []definition.QueryParameter{{Name: "as", Template: `"json"&<x>`}},
			}
		}, []string{`<form id="next" method="get" action="https://api.example.test/v1/tokens/show?as=%22json%22%26%3Cx%3E">`}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.ui.interactive = true
			f.ui.answers["Paste the token"] = "pasted-token"
			f.api.valid("pasted-token")
			tt.change(f)
			var page string
			f.ui.browse = func(_ context.Context, address string) error {
				login, _ := url.Parse(address)
				_, page = visit(t, http.MethodGet, login.Query().Get("redirectUri"))
				return nil
			}
			if _, err := f.manager().Login(context.Background(), "sso"); err != nil {
				t.Fatalf("Login(): %v", err)
			}
			for _, part := range tt.want {
				if !strings.Contains(page, part) {
					t.Errorf("the page lacks %s:\n%s", part, page)
				}
			}
			if tt.lacks != "" && strings.Contains(page, tt.lacks) {
				t.Errorf("the page holds %s:\n%s", tt.lacks, page)
			}
		})
	}
}

// TestPastedSession covers what a user may hand over after a browser
// login: a token, a JSON string, or a JSON object with the fields of the
// definition.
func TestPastedSession(t *testing.T) {
	tests := []struct {
		name   string
		pasted string
		stored string // "" means that the login fails
	}{
		{"a token", "  pasted-token\n", record("pasted-token", "", "")},
		{"a token that is a number", "12345", record("12345", "", "")},
		{"a JSON string", ` "pasted-token" `, record("pasted-token", "", "")},
		{"an object with the first field", `{"accessToken": "pasted-token", "token": "other-token"}`, record("pasted-token", "", "")},
		{"an object with the second field", `{"accessToken": "", "token": " pasted-token "}`, record("pasted-token", "", "")},
		{"an object with a session", `{"token": "pasted-token", "refreshToken": "pasted-refresh-token", "expiresAt": "2030-03-01T00:00:00Z", "expires": "2031-01-01T00:00:00Z"}`,
			record("pasted-token", "pasted-refresh-token", "2030-03-01T00:00:00Z")},
		{"an object with an expiry that is no time", `{"token": "pasted-token", "refreshToken": "pasted-refresh-token", "expiresAt": 1893456000}`,
			record("pasted-token", "pasted-refresh-token", "")},
		{"an object without a token", `{"id": "s-1"}`, ""},
		{"an object whose token is no string", `{"token": 5}`, ""},
		{"an empty JSON string", `""`, ""},
		{"nothing", " \n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.ui.interactive = true
			f.ui.answers["Paste the token"] = tt.pasted
			f.api.valid("pasted-token", "12345")
			var callback string
			f.ui.browse = comeBack(t, &callback)

			_, err := f.manager().Login(context.Background(), "sso")
			if tt.stored == "" {
				if err == nil || apikit.KindOf(err) != apikit.KindUnauthenticated || !strings.Contains(err.Error(), `login "sso" yielded no token`) {
					t.Errorf("Login() error = %v, want that the login yielded no token", err)
				}
				if len(f.api.sent(checkRoute)) != 0 {
					t.Errorf("validated %v", f.api.tokens(checkRoute))
				}
			} else if err != nil {
				t.Errorf("Login(): %v", err)
			}
			if got := f.store.stored(); got != tt.stored {
				t.Errorf("stored %s, want %s", got, tt.stored)
			}
		})
	}
}

// TestLoginKeepsTheRefreshTokenOfItsToken covers a login that yields the
// token that is stored already, without its refresh token: the session
// must stay one that can be renewed.
func TestLoginKeepsTheRefreshTokenOfItsToken(t *testing.T) {
	tests := []struct {
		name    string
		pasted  string
		stored  string
		notices int // besides the one about the browser
	}{
		{"the stored token keeps its refresh token and its expiry", "stored-token", record("stored-token", "stored-refresh-token", "2030-02-01T00:00:00Z"), 0},
		{"the stored token takes a new expiry", `{"token": "stored-token", "expiresAt": "2030-04-01T00:00:00Z"}`, record("stored-token", "stored-refresh-token", "2030-04-01T00:00:00Z"), 0},
		{"the stored token takes a new refresh token", `{"token": "stored-token", "refreshToken": "pasted-refresh-token"}`, record("stored-token", "pasted-refresh-token", ""), 0},
		{"another token does not inherit them", "pasted-token", record("pasted-token", "", ""), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.ui.interactive = true
			f.store.put(record("stored-token", "stored-refresh-token", "2030-02-01T00:00:00Z"))
			f.ui.answers["Paste the token"] = tt.pasted
			f.api.valid("stored-token", "pasted-token")
			var callback string
			f.ui.browse = comeBack(t, &callback)

			if _, err := f.manager().Login(context.Background(), "sso"); err != nil {
				t.Fatalf("Login(): %v", err)
			}
			if got := f.store.stored(); got != tt.stored {
				t.Errorf("stored %s, want %s", got, tt.stored)
			}
			if got := len(f.ui.notices) - 1; got != tt.notices {
				t.Errorf("notices = %q, want %d about a session that cannot be renewed", f.ui.notices, tt.notices)
			}
		})
	}
}

func TestBrowserLoginThatDoesNotComeBack(t *testing.T) {
	t.Run("times out", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.options.Config.Login.Methods[1].TimeoutSeconds = 0.05

		_, err := f.manager().Login(context.Background(), "sso")
		if err == nil || apikit.KindOf(err) != apikit.KindUnauthenticated || !strings.Contains(err.Error(), `login "sso" timed out`) {
			t.Errorf("Login() error = %v, want a timeout", err)
		}
		if len(f.ui.opened) != 1 || len(f.ui.asked) != 0 {
			t.Errorf("opened %v, asked %v", f.ui.opened, f.ui.asked)
		}
	})

	t.Run("ends with the run", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		ctx, cancel := context.WithCancel(context.Background())
		f.ui.browse = func(context.Context, string) error {
			cancel()
			return nil
		}
		_, err := f.manager().Login(ctx, "sso")
		if !errors.Is(err, context.Canceled) || apikit.KindOf(err) != apikit.KindCanceled {
			t.Errorf("Login() error = %v, want the cancellation", err)
		}
	})

	t.Run("goes on when the browser does not open", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.ui.answers["Paste the token"] = "pasted-token"
		f.api.valid("pasted-token")
		var callback string
		follow := comeBack(t, &callback)
		f.ui.browse = func(ctx context.Context, address string) error {
			// The user follows the address in the notice by hand.
			_ = follow(ctx, address)
			return errors.New("no browser found")
		}
		if _, err := f.manager().Login(context.Background(), "sso"); err != nil {
			t.Errorf("Login(): %v", err)
		}
	})

	t.Run("fails on an API the environment does not have", func(t *testing.T) {
		for name, change := range map[string]func(*definition.LoginMethod){
			"for the login page":     func(method *definition.LoginMethod) { method.URL.API = "other" },
			"for the form to submit": func(method *definition.LoginMethod) { method.Submit.API = "other" },
		} {
			f := newFixture(t)
			f.ui.interactive = true
			change(&f.options.Config.Login.Methods[1])
			_, err := f.manager().Login(context.Background(), "sso")
			if err == nil || !strings.Contains(err.Error(), `no API "other"`) || len(f.ui.opened) != 0 {
				t.Errorf("%s: Login() error = %v, opened %v", name, err, f.ui.opened)
			}
		}
	})
}

func TestBrowserLoginWithoutASession(t *testing.T) {
	t.Run("the user does not answer", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.ui.abort = errors.New("interrupted")
		var callback string
		f.ui.browse = comeBack(t, &callback)
		_, err := f.manager().Login(context.Background(), "sso")
		if !errors.Is(err, f.ui.abort) || len(f.api.calls) != 0 {
			t.Errorf("Login() = %v after %d requests", err, len(f.api.calls))
		}
	})

	t.Run("the token does not match the pattern", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.options.Config.Login.Methods[1].Session.Token.Match = regexp.MustCompile(`^s-\w+`)
		f.ui.answers["Paste the token"] = `{"accessToken": "pasted-token"}`
		var callback string
		f.ui.browse = comeBack(t, &callback)
		_, err := f.manager().Login(context.Background(), "sso")
		if err == nil || apikit.KindOf(err) != apikit.KindUnauthenticated || !strings.Contains(err.Error(), "the value of accessToken does not match") || strings.Contains(err.Error(), "pasted-token") {
			t.Errorf("Login() error = %v", err)
		}
	})
}
