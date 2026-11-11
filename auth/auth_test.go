package auth_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/definition"
)

func TestNewChecksItsOptions(t *testing.T) {
	tests := []struct {
		name   string
		change func(*auth.Options)
		want   string
	}{
		{"no definition", func(o *auth.Options) { o.Config = definition.Auth{} }, "Options.Config"},
		{"no base URLs", func(o *auth.Options) { o.BaseURL = nil }, "Options.BaseURL"},
		{"store without an account", func(o *auth.Options) { o.Account = "" }, "Options.Account"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.change(&f.options)
			if _, err := auth.New(f.options); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New error = %v, want it to name %s", err, tt.want)
			}
		})
	}

	t.Run("only what the definition needs", func(t *testing.T) {
		f := newFixture(t)
		_, err := auth.New(auth.Options{Config: f.options.Config, BaseURL: f.options.BaseURL})
		if err != nil {
			t.Errorf("New with the defaults: %v", err)
		}
	})
}

func TestAuthorizeSetsTheHeaderOfTheDefinition(t *testing.T) {
	f := newFixture(t)
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.test/v1/pets", nil)
	f.manager().Authorize(req, "stored-token")
	if got := req.Header.Get("X-Token"); got != "stored-token" {
		t.Errorf("X-Token = %q", got)
	}

	doc, _, err := definition.Load(definition.Source{Name: "plain.yaml", Data: []byte("environments: {production: {apis: {main: https://api.example.test}}}")}, definition.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.options.Config = doc.Auth
	req, _ = http.NewRequest(http.MethodGet, "https://api.example.test/pets", nil)
	f.manager().Authorize(req, "stored-token")
	if got := req.Header.Get("Authorization"); got != "Bearer stored-token" {
		t.Errorf("Authorization = %q, want the default of a definition without an auth section", got)
	}
}

// TestTokenTakesTheFirstCandidateUnchecked covers the order of the sources
// and that an ordinary request costs no round trip for its token.
func TestTokenTakesTheFirstCandidateUnchecked(t *testing.T) {
	// A value that was exported with white space around it.
	const padded = " env-token "
	tests := []struct {
		name     string
		explicit string
		stored   string
		env      map[string]string
		want     string
	}{
		{"the token given for the run goes before everything", " given-token ", "stored-token", map[string]string{"EXAMPLE_TOKEN": "env-token"}, "given-token"},
		{"the store outranks the environment", "", record("stored-token", "", ""), map[string]string{"EXAMPLE_TOKEN": "env-token"}, "stored-token"},
		{"a stored value that is no JSON is a token", "", " stored-token\n", nil, "stored-token"},
		{"environment variables in the order of the definition", "", "", map[string]string{"EXAMPLE_CI_TOKEN": "ci-token", "EXAMPLE_TOKEN": padded}, "env-token"},
		{"an empty variable is skipped", "", "", map[string]string{"EXAMPLE_TOKEN": " ", "EXAMPLE_CI_TOKEN": "ci-token"}, "ci-token"},
		{"a stored session without a token is skipped", "", `{"refreshToken": "stored-refresh-token"}`, map[string]string{"EXAMPLE_TOKEN": "env-token"}, "env-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.options.Explicit = tt.explicit
			if tt.stored != "" {
				f.store.put(tt.stored)
			}
			f.env = tt.env
			m := f.manager()

			for range 2 {
				token, err := m.Token(context.Background())
				if err != nil || token != tt.want {
					t.Fatalf("Token() = %q, %v; want %q", token, err, tt.want)
				}
			}
			if len(f.api.calls) != 0 {
				t.Errorf("Token() sent %v, want no request", f.api.calls)
			}
			// The second call answers from memory, and a token given for
			// the run leaves the store unread altogether.
			want := 1
			if tt.explicit != "" {
				want = 0
			}
			if f.store.reads != want {
				t.Errorf("the store was read %d times, want %d", f.store.reads, want)
			}
		})
	}
}

func TestTokenWithoutACandidate(t *testing.T) {
	t.Run("says that a login is needed", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.manager().Token(context.Background())
		wantUnauthenticated(t, err, true, false, false)
		if !strings.Contains(err.Error(), "a login is needed") {
			t.Errorf("Token() error = %q, want it to say that a login is needed", err)
		}
		if len(f.ui.asked) != 0 || len(f.api.calls) != 0 {
			t.Errorf("asked %v and sent %v on a run that is not interactive", f.ui.asked, f.api.calls)
		}
	})

	t.Run("names no login where the definition has none", func(t *testing.T) {
		f := newFixture(t)
		f.options.Config.Login = definition.Login{}
		_, err := f.manager().Token(context.Background())
		wantUnauthenticated(t, err, true, false, false)
		if err.Error() != auth.ErrNoToken.Error() {
			t.Errorf("Token() error = %q, want %q", err, auth.ErrNoToken)
		}
	})

	t.Run("never asks without a user", func(t *testing.T) {
		f := newFixture(t)
		f.options.UI = nil
		_, err := f.manager().Token(context.Background())
		wantUnauthenticated(t, err, true, false, false)
	})

	t.Run("logs in on an interactive run", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.ui.answers = map[string]string{"Username": "ada@example.test", "Password": "hunter-secret"}
		f.api.on(loginRoute, func(call) reply {
			return reply{status: 200, body: `{"token": "left-token:right-token:rest", "refreshToken": "login-refresh-token"}`}
		})
		f.api.valid("left-token:right-token")
		m := f.manager()

		token, err := m.Token(context.Background())
		if err != nil || token != "left-token:right-token" {
			t.Fatalf("Token() = %q, %v", token, err)
		}
		if len(f.ui.notices) != 1 || !strings.Contains(f.ui.notices[0], "No valid API token is available") {
			t.Errorf("notices = %q, want one that says why the user is asked", f.ui.notices)
		}
		if got := f.store.stored(); got != record("left-token:right-token", "login-refresh-token", "") {
			t.Errorf("stored %s", got)
		}
		// The login has proven the token: nothing is asked again.
		before := len(f.api.calls)
		if again, _, err := m.Resolve(context.Background(), true); err != nil || again != token {
			t.Errorf("Resolve() after the login = %q, %v", again, err)
		}
		if len(f.api.calls) != before || len(f.ui.asked) != 2 {
			t.Errorf("the token of the login was put to the API or asked for again")
		}
	})

	t.Run("fails with the error of the login", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.ui.answers = map[string]string{"Username": "ada@example.test", "Password": "hunter-secret"}
		f.api.on(loginRoute, func(call) reply { return reply{status: 401, body: `{"message": "wrong credentials"}`} })

		_, source, err := f.manager().Resolve(context.Background(), true)
		wantUnauthenticated(t, err, false, false, false)
		if source != (auth.Source{}) || !strings.Contains(err.Error(), "wrong credentials") {
			t.Errorf("Resolve() = %v, %v", source, err)
		}
	})

	t.Run("does not log in when it is told not to", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		_, _, err := f.manager().Resolve(context.Background(), false)
		wantUnauthenticated(t, err, true, false, false)
		if len(f.ui.asked) != 0 {
			t.Errorf("asked %v", f.ui.asked)
		}
	})

	t.Run("does not log in without a fallback method", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.options.Config.Login.Fallback = ""
		_, err := f.manager().Token(context.Background())
		wantUnauthenticated(t, err, true, false, false)
		if len(f.ui.asked) != 0 {
			t.Errorf("asked %v", f.ui.asked)
		}
	})
}

// TestValidationVerdicts covers the three answers of the validation, and
// the request it sends.
func TestValidationVerdicts(t *testing.T) {
	tests := []struct {
		name         string
		answer       reply
		valid        bool
		inconclusive bool
	}{
		{"200 proves the token good", reply{status: 200}, true, false},
		{"204 proves the token good", reply{status: 204}, true, false},
		{"401 proves the token bad", reply{status: 401}, false, false},
		{"403 is about permissions, not about the token", reply{status: 403}, false, true},
		{"429 proves nothing", reply{status: 429}, false, true},
		{"500 proves nothing", reply{status: 500}, false, true},
		{"no answer proves nothing", reply{err: errors.New("connection refused")}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.options.Explicit = "given-token"
			f.api.on(checkRoute, func(call) reply { return tt.answer })

			token, source, err := f.manager().Resolve(context.Background(), true)
			switch {
			case tt.valid:
				if err != nil || token != "given-token" || source != (auth.Source{Kind: auth.SourceExplicit}) {
					t.Errorf("Resolve() = %q, %v, %v", token, source, err)
				}
			default:
				wantUnauthenticated(t, err, !tt.inconclusive, tt.inconclusive, false)
				if token != "" || source != (auth.Source{}) {
					t.Errorf("Resolve() = %q, %v beside an error", token, source)
				}
			}

			want := call{Route: checkRoute, URL: "https://api.example.test/v1/tokens/check", Header: "given-token", Body: "{}"}
			if got := f.api.sent(checkRoute); !slices.Equal(got, []call{want}) {
				t.Errorf("validation requests = %+v, want %+v", got, want)
			}
		})
	}
}

func TestResolveTriesTheCandidatesInOrder(t *testing.T) {
	f := newFixture(t)
	f.store.put("stored-token")
	f.env = map[string]string{"EXAMPLE_TOKEN": "env-token", "EXAMPLE_CI_TOKEN": "ci-token", "EXAMPLE_FIXED_TOKEN": "env-token"}
	f.api.valid("ci-token")
	m := f.manager()

	token, source, err := m.Resolve(context.Background(), false)
	if err != nil || token != "ci-token" || source != (auth.Source{Kind: auth.SourceEnv, Name: "EXAMPLE_CI_TOKEN"}) {
		t.Fatalf("Resolve() = %q, %v, %v", token, source, err)
	}
	// A token that two variables hold is tried once.
	if got, want := f.api.tokens(checkRoute), []string{"stored-token", "env-token", "ci-token"}; !slices.Equal(got, want) {
		t.Errorf("validated %v, want %v", got, want)
	}

	// The token is settled: later calls ask neither the API nor the store.
	reads := f.store.reads
	token, source, err = m.Resolve(context.Background(), false)
	if err != nil || token != "ci-token" || source != (auth.Source{Kind: auth.SourceProcess}) {
		t.Errorf("second Resolve() = %q, %v, %v", token, source, err)
	}
	if again, err := m.Token(context.Background()); err != nil || again != "ci-token" {
		t.Errorf("Token() = %q, %v", again, err)
	}
	if len(f.api.calls) != 3 || f.store.reads != reads {
		t.Errorf("a settled token cost %d requests and %d reads of the store", len(f.api.calls)-3, f.store.reads-reads)
	}
	// The stored token was rejected and is kept all the same.
	if got := f.store.stored(); got != "stored-token" {
		t.Errorf("stored %q, want the rejected token left alone", got)
	}
}

func TestResolveReportsTheSource(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*fixture)
		token  string
		source auth.Source
	}{
		{"the token given for the run", func(f *fixture) { f.options.Explicit = "given-token" }, "given-token", auth.Source{Kind: auth.SourceExplicit}},
		{"the store", func(f *fixture) { f.store.put(record("stored-token", "", "")) }, "stored-token", auth.Source{Kind: auth.SourceStore}},
		{"an environment variable", func(f *fixture) { f.env["EXAMPLE_FIXED_TOKEN"] = "fixed-token" }, "fixed-token", auth.Source{Kind: auth.SourceEnv, Name: "EXAMPLE_FIXED_TOKEN"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.setup(f)
			f.api.valid(tt.token)
			m := f.manager()

			source, validated, err := m.Status(context.Background())
			if err != nil || source != tt.source || !validated {
				t.Errorf("Status() = %v, %v, %v; want %v, validated", source, validated, err, tt.source)
			}
			if source.String() == "" || source.String() == "none" {
				t.Errorf("Source.String() = %q", source.String())
			}
			token, _, err := m.Resolve(context.Background(), false)
			if err != nil || token != tt.token {
				t.Errorf("Resolve() = %q, %v", token, err)
			}
		})
	}
}

func TestStatusNeverAsks(t *testing.T) {
	f := newFixture(t)
	f.ui.interactive = true
	source, validated, err := f.manager().Status(context.Background())
	wantUnauthenticated(t, err, true, false, false)
	if source != (auth.Source{}) || validated || len(f.ui.asked) != 0 || len(f.ui.notices) != 0 {
		t.Errorf("Status() = %v, %v; asked %v", source, validated, f.ui.asked)
	}
}

func TestSourceString(t *testing.T) {
	tests := map[auth.Source]string{
		{}:                                    "none",
		{Kind: auth.SourceExplicit}:           "explicit token",
		{Kind: auth.SourceProcess}:            "token of this process",
		{Kind: auth.SourceStore}:              "stored session",
		{Kind: auth.SourceRefresh}:            "refreshed session",
		{Kind: auth.SourceLogin, Name: "sso"}: "login sso",
		{Kind: auth.SourceEnv, Name: "EXAMPLE_TOKEN"}: "environment variable EXAMPLE_TOKEN",
	}
	for source, want := range tests {
		if got := source.String(); got != want {
			t.Errorf("String() of %#v = %q, want %q", source, got, want)
		}
	}
}

// TestInconclusiveValidation covers a validation that gives no verdict: it
// must cost neither the stored token nor a prompt.
func TestInconclusiveValidation(t *testing.T) {
	f := newFixture(t)
	f.ui.interactive = true
	f.store.put(record("stored-token", "stored-refresh-token", ""))
	f.api.verdicts["stored-token"] = http.StatusServiceUnavailable
	m := f.manager()

	_, _, err := m.Resolve(context.Background(), true)
	wantUnauthenticated(t, err, false, true, false)
	if len(f.ui.asked) != 0 || len(f.ui.notices) != 0 {
		t.Errorf("asked %v after a validation without a verdict", f.ui.asked)
	}
	if got := f.store.stored(); got != record("stored-token", "stored-refresh-token", "") {
		t.Errorf("stored %s, want the session untouched", got)
	}
	if len(f.api.sent(refreshRoute)) != 0 {
		t.Error("the session was refreshed without a verdict on its token")
	}
	// The token was not given up: a request may still try it.
	if token, err := m.Token(context.Background()); err != nil || token != "stored-token" {
		t.Errorf("Token() = %q, %v", token, err)
	}
}

func TestResolveSkipsATokenWithoutAVerdict(t *testing.T) {
	f := newFixture(t)
	f.store.put("stored-token")
	f.env["EXAMPLE_TOKEN"] = "env-token"
	f.api.verdicts["stored-token"] = http.StatusInternalServerError
	f.api.valid("env-token")

	token, source, err := f.manager().Resolve(context.Background(), false)
	if err != nil || token != "env-token" || source.Kind != auth.SourceEnv {
		t.Errorf("Resolve() = %q, %v, %v", token, source, err)
	}
}

func TestExplicitTokenIsTheOnlyCandidate(t *testing.T) {
	f := newFixture(t)
	f.ui.interactive = true
	f.options.Explicit = "given-token"
	f.store.put(record("given-token", "stored-refresh-token", ""))
	// The same token is stored and exported, each with a refresh token.
	f.export("given-token", "env-refresh-token")
	f.env["EXAMPLE_CI_TOKEN"] = "ci-token"
	f.api.valid("ci-token")
	m := f.manager()

	_, _, err := m.Resolve(context.Background(), true)
	wantUnauthenticated(t, err, true, false, false)
	if !strings.Contains(err.Error(), "given for this run") {
		t.Errorf("Resolve() error = %q, want it to name the token given for the run", err)
	}
	if got := f.api.tokens(checkRoute); !slices.Equal(got, []string{"given-token"}) {
		t.Errorf("validated %v, want the given token alone", got)
	}

	// A 401 changes nothing about that: no rotation, no other source, no
	// prompt, and the token stays the one that is sent.
	token, err := m.Token(context.Background())
	if err != nil || token != "given-token" {
		t.Fatalf("Token() = %q, %v", token, err)
	}
	_, err = m.Rejected(context.Background(), token)
	wantUnauthenticated(t, err, true, false, false)
	if token, err := m.Token(context.Background()); err != nil || token != "given-token" {
		t.Errorf("Token() after the rejection = %q, %v", token, err)
	}
	if len(f.api.sent(refreshRoute)) != 0 || len(f.api.sent(checkRoute)) != 1 || len(f.ui.asked) != 0 || f.store.reads != 0 {
		t.Errorf("requests %v, prompts %v, %d reads of the store", f.api.calls, f.ui.asked, f.store.reads)
	}
}

func TestWithoutValidationEveryTokenIsUsable(t *testing.T) {
	f := newFixture(t)
	f.options.Config.Validate = nil
	f.store.put("stored-token")
	f.env["EXAMPLE_TOKEN"] = "env-token"
	m := f.manager()

	token, source, err := m.Resolve(context.Background(), false)
	if err != nil || token != "stored-token" || source.Kind != auth.SourceStore {
		t.Fatalf("Resolve() = %q, %v, %v", token, source, err)
	}
	source, validated, err := m.Status(context.Background())
	if err != nil || source.Kind != auth.SourceProcess || validated {
		t.Errorf("Status() = %v, %v, %v; want a token that is not validated", source, validated, err)
	}

	// A request that succeeds is the proof.
	m.Confirmed("another-token")
	if _, validated, _ := m.Status(context.Background()); validated {
		t.Error("a request with another token confirmed this one")
	}
	m.Confirmed("stored-token")
	if _, validated, _ := m.Status(context.Background()); !validated {
		t.Error("Status() is not validated after Confirmed")
	}

	// A rejection moves on to the next source, unchecked as well.
	f = newFixture(t)
	f.options.Config.Validate = nil
	f.store.put("stored-token")
	f.env["EXAMPLE_TOKEN"] = "env-token"
	m = f.manager()
	if token, err := m.Rejected(context.Background(), "stored-token"); err != nil || token != "env-token" {
		t.Errorf("Rejected() = %q, %v", token, err)
	}
	if token, err := m.Rejected(context.Background(), "stored-token"); err != nil || token != "env-token" {
		t.Errorf("second Rejected() = %q, %v", token, err)
	}
	if len(f.api.calls) != 0 {
		t.Errorf("sent %v", f.api.calls)
	}
}

func TestCanceledRunFailsAsCanceled(t *testing.T) {
	f := newFixture(t)
	f.store.put("stored-token")
	ctx, cancel := context.WithCancel(context.Background())
	f.api.on(checkRoute, func(call) reply {
		cancel()
		return reply{err: ctx.Err()}
	})
	_, _, err := f.manager().Resolve(ctx, false)
	if !errors.Is(err, context.Canceled) || apikit.KindOf(err) != apikit.KindCanceled {
		t.Errorf("Resolve() error = %v, want the cancellation", err)
	}
}
