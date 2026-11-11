package auth_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mittwald/api-kit/auth"
)

// rotated is the session the fake API hands out for a refresh token.
const rotated = `{"token": "rotated-token", "refreshToken": "rotated-refresh-token", "expiresAt": "2030-01-02T00:00:00Z"}`

func TestResolveRefreshesARejectedSessionBeforeItGivesItUp(t *testing.T) {
	f := newFixture(t)
	f.store.put(record("stored-token", "stored-refresh-token", ""))
	f.api.sessions["stored-refresh-token"] = rotated
	m := f.manager()

	token, source, err := m.Resolve(context.Background(), false)
	if err != nil || token != "rotated-token" || source != (auth.Source{Kind: auth.SourceRefresh}) {
		t.Fatalf("Resolve() = %q, %v, %v", token, source, err)
	}
	// The refresh carries the old token in the header of the definition
	// and the refresh token in the body.
	want := call{Route: refreshRoute, URL: "https://api.example.test/v1/sessions", Header: "stored-token", Body: `{"refreshToken":"stored-refresh-token"}`}
	if got := f.api.sent(refreshRoute); !slices.Equal(got, []call{want}) {
		t.Errorf("refresh requests = %+v, want %+v", got, want)
	}
	if got, want := f.store.stored(), record("rotated-token", "rotated-refresh-token", "2030-01-02T00:00:00Z"); got != want {
		t.Errorf("stored %s, want %s", got, want)
	}
	// The new token is trusted as it is.
	if got := f.api.tokens(checkRoute); !slices.Equal(got, []string{"stored-token"}) {
		t.Errorf("validated %v, want the stored token alone", got)
	}
	if _, validated, err := m.Status(context.Background()); err != nil || !validated {
		t.Errorf("Status() after the refresh: validated %v, %v", validated, err)
	}
}

// TestRefreshOutcomes covers what a refresh can come back with. Only a new
// token rotates the session; in every other case the stored token is kept
// and reported as stale.
func TestRefreshOutcomes(t *testing.T) {
	before := record("stored-token", "stored-refresh-token", "")
	tests := []struct {
		name    string
		answer  reply
		rotated bool
		stored  string
	}{
		{"a session without a refresh token is stored all the same", reply{status: 200, body: `{"token": "rotated-token"}`}, true, record("rotated-token", "", "")},
		{"any 2xx counts", reply{status: 201, body: rotated}, true, record("rotated-token", "rotated-refresh-token", "2030-01-02T00:00:00Z")},
		{"400 says the session is gone", reply{status: 400}, false, before},
		{"401 says the session is gone", reply{status: 401}, false, before},
		{"403 says the session is gone", reply{status: 403}, false, before},
		{"a server error", reply{status: 502}, false, before},
		{"no answer", reply{err: errors.New("connection reset")}, false, before},
		{"a response without a token", reply{status: 200, body: `{"refreshToken": "rotated-refresh-token"}`}, false, before},
		{"a response that is no object", reply{status: 200, body: `"rotated-token"`}, false, before},
		{"the rejected token once more", reply{status: 200, body: `{"token": "stored-token", "refreshToken": "rotated-refresh-token"}`}, false, record("stored-token", "rotated-refresh-token", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.put(before)
			f.api.on(refreshRoute, func(call) reply { return tt.answer })
			m := f.manager()

			token, err := m.Rejected(context.Background(), "stored-token")
			if !tt.rotated {
				wantUnauthenticated(t, err, true, false, true)
			} else if err != nil || token != "rotated-token" {
				t.Errorf("Rejected() = %q, %v", token, err)
			}
			if got := f.store.stored(); got != tt.stored {
				t.Errorf("stored %s, want %s", got, tt.stored)
			}
			if len(f.api.sent(refreshRoute)) != 1 {
				t.Errorf("refresh requests = %v, want one", f.api.sent(refreshRoute))
			}
		})
	}
}

func TestStaleStoredToken(t *testing.T) {
	t.Run("is kept and reported by Resolve", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "stored-refresh-token", ""))
		m := f.manager()

		_, _, err := m.Resolve(context.Background(), false)
		wantUnauthenticated(t, err, true, false, true)
		if got := f.store.stored(); got != record("stored-token", "stored-refresh-token", "") {
			t.Errorf("stored %s, want the session kept", got)
		}
		// It is not offered again, and stays reported.
		_, err = m.Token(context.Background())
		wantUnauthenticated(t, err, true, false, true)
		if got := f.api.tokens(checkRoute); len(got) != 1 {
			t.Errorf("validated %v, want the stored token once", got)
		}
	})

	t.Run("is reported after a 401", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "stored-refresh-token", ""))
		m := f.manager()

		token, err := m.Token(context.Background())
		if err != nil || token != "stored-token" {
			t.Fatalf("Token() = %q, %v", token, err)
		}
		_, err = m.Rejected(context.Background(), token)
		wantUnauthenticated(t, err, true, false, true)
		if len(f.api.sent(checkRoute)) != 0 {
			t.Error("a token the API has just rejected was validated")
		}
	})

	t.Run("is not reported when another source works", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "stored-refresh-token", ""))
		f.env["EXAMPLE_CI_TOKEN"] = "ci-token"
		f.api.valid("ci-token")
		m := f.manager()

		token, err := m.Rejected(context.Background(), "stored-token")
		if err != nil || token != "ci-token" {
			t.Errorf("Rejected() = %q, %v", token, err)
		}
		if got := f.api.tokens(checkRoute); !slices.Equal(got, []string{"ci-token"}) {
			t.Errorf("validated %v, want the other token alone", got)
		}
	})

	t.Run("is reported beside a validation without a verdict", func(t *testing.T) {
		f := newFixture(t)
		f.ui.interactive = true
		f.store.put("stored-token")
		f.env["EXAMPLE_TOKEN"] = "env-token"
		f.api.verdicts["env-token"] = 503

		_, err := f.manager().Rejected(context.Background(), "stored-token")
		wantUnauthenticated(t, err, true, true, true)
		if len(f.ui.asked) != 0 {
			t.Errorf("asked %v", f.ui.asked)
		}
	})

	t.Run("is one without a refresh token as well", func(t *testing.T) {
		f := newFixture(t)
		f.store.put("stored-token")
		_, err := f.manager().Rejected(context.Background(), "stored-token")
		wantUnauthenticated(t, err, true, false, true)
		if len(f.api.calls) != 0 {
			t.Errorf("sent %v, want nothing: there is no refresh token to redeem", f.api.calls)
		}
	})

	t.Run("cannot be refreshed without a refresh request", func(t *testing.T) {
		f := newFixture(t)
		f.options.Config.Refresh = nil
		f.store.put(record("stored-token", "stored-refresh-token", "2029-12-31T00:00:00Z"))
		m := f.manager()
		if token, err := m.Token(context.Background()); err != nil || token != "stored-token" {
			t.Fatalf("Token() = %q, %v", token, err)
		}
		_, err := m.Rejected(context.Background(), "stored-token")
		wantUnauthenticated(t, err, true, false, true)
		if len(f.api.calls) != 0 {
			t.Errorf("sent %v", f.api.calls)
		}
	})
}

// TestRejectedRefreshesTheSession covers the 401 of a request: the only
// sign of an expiry that a session without a known one ever gives.
func TestRejectedRefreshesTheSession(t *testing.T) {
	tests := []struct {
		name     string
		stored   string
		rejected string // the token the API rejects
		refresh  string // the refresh token that belongs to it
	}{
		{"of the store", record("stored-token", "stored-refresh-token", ""), "stored-token", "stored-refresh-token"},
		{"of the first variable", "", "env-token", "env-refresh-token"},
		{"of the second variable", "", "ci-token", "ci-refresh-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.stored != "" {
				f.store.put(tt.stored)
			}
			// Both variables hold a session; which one a token belongs to
			// shows by its value alone.
			f.export("env-token", " env-refresh-token ")
			f.env["EXAMPLE_CI_TOKEN"] = "ci-token"
			f.env["EXAMPLE_CI_REFRESH_TOKEN"] = "ci-refresh-token"
			f.api.sessions[tt.refresh] = rotated
			m := f.manager()

			token, err := m.Rejected(context.Background(), tt.rejected)
			if err != nil || token != "rotated-token" {
				t.Fatalf("Rejected() = %q, %v", token, err)
			}
			refreshes := f.api.sent(refreshRoute)
			if len(refreshes) != 1 || refreshes[0].Header != tt.rejected || refreshes[0].Body != `{"refreshToken":"`+tt.refresh+`"}` {
				t.Errorf("refresh requests = %+v", refreshes)
			}
			if len(f.api.sent(checkRoute)) != 0 {
				t.Errorf("validated %v, want no validation at all", f.api.tokens(checkRoute))
			}
			// A session that started in the environment goes on in the
			// store, which is where the next run finds it.
			if got, want := f.store.stored(), record("rotated-token", "rotated-refresh-token", "2030-01-02T00:00:00Z"); got != want {
				t.Errorf("stored %s, want %s", got, want)
			}
			// The rejected token is gone for this process, the new one is
			// settled.
			if again, err := m.Token(context.Background()); err != nil || again != "rotated-token" {
				t.Errorf("Token() after the rotation = %q, %v", again, err)
			}
		})
	}
}

func TestRejectedFallsBackToAnotherSource(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"when the variable has no refresh token", map[string]string{"EXAMPLE_TOKEN": "env-token", "EXAMPLE_CI_TOKEN": "ci-token"}},
		{"when the definition pairs the variable with none", map[string]string{"EXAMPLE_FIXED_TOKEN": "env-token", "EXAMPLE_CI_TOKEN": "ci-token", "EXAMPLE_CI_REFRESH_TOKEN": "ci-refresh-token"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.env = tt.env
			f.api.valid("ci-token")
			f.api.sessions["ci-refresh-token"] = rotated
			m := f.manager()

			token, err := m.Rejected(context.Background(), "env-token")
			if err != nil || token != "ci-token" {
				t.Errorf("Rejected() = %q, %v", token, err)
			}
			if len(f.api.sent(refreshRoute)) != 0 {
				t.Errorf("refreshed with %v: the refresh token of another token", f.api.sent(refreshRoute))
			}
			// The dead token is not offered again.
			if got := f.api.tokens(checkRoute); !slices.Equal(got, []string{"ci-token"}) {
				t.Errorf("validated %v", got)
			}
		})
	}
}

func TestRejectedTokenThatNoSourceHolds(t *testing.T) {
	f := newFixture(t)
	f.export("env-token", "env-refresh-token")
	f.api.valid("env-token")
	f.api.sessions["env-refresh-token"] = rotated

	token, err := f.manager().Rejected(context.Background(), "unknown-token")
	if err != nil || token != "env-token" {
		t.Errorf("Rejected() = %q, %v", token, err)
	}
	if len(f.api.sent(refreshRoute)) != 0 {
		t.Errorf("refreshed with %v: the refresh token of another token", f.api.sent(refreshRoute))
	}
}

func TestLoginThatYieldsTheRejectedTokenDoesNotHelp(t *testing.T) {
	f := newFixture(t)
	f.ui.interactive = true
	f.ui.answers = credentials()
	f.env["EXAMPLE_FIXED_TOKEN"] = "left-token:right-token"
	f.api.on(loginRoute, func(call) reply {
		return reply{status: 200, body: `{"token": "left-token:right-token", "refreshToken": "login-refresh-token"}`}
	})
	f.api.valid("left-token:right-token")

	_, err := f.manager().Rejected(context.Background(), "left-token:right-token")
	wantUnauthenticated(t, err, true, false, false)
	if len(f.api.sent(loginRoute)) != 1 {
		t.Errorf("login requests = %v, want the fallback to have run", f.api.sent(loginRoute))
	}
}

func TestRejectedTokenIsNotOfferedAgain(t *testing.T) {
	f := newFixture(t)
	f.env["EXAMPLE_TOKEN"] = "env-token"
	m := f.manager()

	token, err := m.Token(context.Background())
	if err != nil || token != "env-token" {
		t.Fatalf("Token() = %q, %v", token, err)
	}
	_, err = m.Rejected(context.Background(), token)
	wantUnauthenticated(t, err, true, false, false)
	_, err = m.Token(context.Background())
	wantUnauthenticated(t, err, true, false, false)
	if _, _, err = m.Resolve(context.Background(), false); err == nil {
		t.Error("Resolve() offers a token the API rejected")
	}
	if len(f.api.calls) != 0 {
		t.Errorf("sent %v, want nothing: a rejected token is not asked about", f.api.calls)
	}
}

func TestLaterRejectionReusesTheTokenOfTheFirst(t *testing.T) {
	f := newFixture(t)
	f.store.put(record("stored-token", "stored-refresh-token", ""))
	f.api.sessions["stored-refresh-token"] = rotated
	m := f.manager()

	for i := range 3 {
		token, err := m.Rejected(context.Background(), "stored-token")
		if err != nil || token != "rotated-token" {
			t.Errorf("Rejected() number %d = %q, %v", i+1, token, err)
		}
	}
	if got := len(f.api.sent(refreshRoute)); got != 1 {
		t.Errorf("%d refresh requests, want 1", got)
	}
}

// TestConcurrentRejectionsRedeemARefreshTokenOnce holds the first rotation
// open while other goroutines are rejected with the same token. A refresh
// token is good for one use: whoever redeems it a second time loses the
// session.
func TestConcurrentRejectionsRedeemARefreshTokenOnce(t *testing.T) {
	f := newFixture(t)
	f.store.put(record("stored-token", "stored-refresh-token", ""))
	entered := make(chan struct{})
	release := make(chan struct{})
	var enter sync.Once
	f.api.on(refreshRoute, func(received call) reply {
		// Only the first redemption is held open. Without the lock the
		// others would get here too and be answered at once.
		first := false
		enter.Do(func() { first = true })
		if first {
			close(entered)
			<-release
			return reply{status: 200, body: rotated}
		}
		return reply{status: 400}
	})
	m := f.manager()

	const callers = 8
	results := make(chan string, callers)
	reject := func() {
		token, err := m.Rejected(context.Background(), "stored-token")
		if err != nil {
			token = "error: " + err.Error()
		}
		results <- token
	}
	go reject()
	<-entered
	for range callers - 1 {
		go reject()
	}
	// The rotation holds the lock, so nobody has an answer yet.
	select {
	case token := <-results:
		t.Errorf("a caller got %q while the rotation was under way", token)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	for range callers {
		select {
		case token := <-results:
			if token != "rotated-token" {
				t.Errorf("Rejected() = %q, want the rotated token", token)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a caller never returned")
		}
	}
	if got := len(f.api.sent(refreshRoute)); got != 1 {
		t.Errorf("%d refresh requests, want 1", got)
	}
	if len(f.api.sent(checkRoute)) != 0 {
		t.Errorf("validated %v", f.api.tokens(checkRoute))
	}
}

// TestRefreshAheadOfExpiry covers a stored session that is renewed before
// it is used, and the spellings of an expiry.
func TestRefreshAheadOfExpiry(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		refreshed bool
	}{
		{"expired long ago", "2000-01-01T00:00:00Z", true},
		{"expires within the leeway", "2030-01-01T00:04:59Z", true},
		{"expires at the end of the leeway", "2030-01-01T00:05:00Z", true},
		{"expires after the leeway", "2030-01-01T00:05:01Z", false},
		{"far from its expiry", "2100-01-01T00:00:00Z", false},
		{"an offset from UTC counts", "2030-01-01T02:04:00+02:00", true},
		{"an offset from UTC counts the other way", "2030-01-01T00:04:00-02:00", false},
		{"no time zone means UTC", "2030-01-01T00:04:00", true},
		{"no time zone means UTC, after the leeway", "2030-01-01T00:06:00", false},
		{"a space between date and time", "2030-01-01 00:04:00", true},
		{"fractions of a second", "2030-01-01T00:04:00.123456Z", true},
		{"a date alone is its first moment", "2030-01-01", true},
		{"a date alone, after the leeway", "2030-01-02", false},
		{"an unknown expiry is not a near one", "", false},
		{"an expiry that cannot be read is an unknown one", "soon", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.put(record("stored-token", "stored-refresh-token", tt.expiresAt))
			f.env["EXAMPLE_TOKEN"] = "env-token"
			f.api.sessions["stored-refresh-token"] = rotated

			want := "stored-token"
			if tt.refreshed {
				want = "rotated-token"
			}
			token, err := f.manager().Token(context.Background())
			if err != nil || token != want {
				t.Errorf("Token() = %q, %v; want %q", token, err, want)
			}
			if got := len(f.api.sent(refreshRoute)); (got == 1) != tt.refreshed {
				t.Errorf("%d refresh requests", got)
			}
			if len(f.api.sent(checkRoute)) != 0 {
				t.Errorf("validated %v", f.api.tokens(checkRoute))
			}
		})
	}

	t.Run("Resolve reports the refresh", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "stored-refresh-token", "2030-01-01T00:01:00Z"))
		f.api.sessions["stored-refresh-token"] = rotated
		token, source, err := f.manager().Resolve(context.Background(), false)
		if err != nil || token != "rotated-token" || source.Kind != auth.SourceRefresh {
			t.Errorf("Resolve() = %q, %v, %v", token, source, err)
		}
		if len(f.api.sent(checkRoute)) != 0 {
			t.Errorf("validated %v, want the refreshed token trusted", f.api.tokens(checkRoute))
		}
	})

	t.Run("the leeway comes from the definition", func(t *testing.T) {
		f := newFixture(t)
		f.options.Config.Refresh.LeewaySeconds = 3600
		f.store.put(record("stored-token", "stored-refresh-token", "2030-01-01T00:59:00Z"))
		f.api.sessions["stored-refresh-token"] = rotated
		if token, err := f.manager().Token(context.Background()); err != nil || token != "rotated-token" {
			t.Errorf("Token() = %q, %v", token, err)
		}
	})

	t.Run("a session without a refresh token is used as it is", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "", "2000-01-01T00:00:00Z"))
		if token, err := f.manager().Token(context.Background()); err != nil || token != "stored-token" {
			t.Errorf("Token() = %q, %v", token, err)
		}
		if len(f.api.calls) != 0 {
			t.Errorf("sent %v", f.api.calls)
		}
	})

	t.Run("a refresh that fails leaves the token to the request", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "stored-refresh-token", "2030-01-01T00:01:00Z"))
		f.api.on(refreshRoute, func(call) reply { return reply{status: 503} })
		if token, err := f.manager().Token(context.Background()); err != nil || token != "stored-token" {
			t.Errorf("Token() = %q, %v", token, err)
		}
	})

	t.Run("a token given for the run is never refreshed", func(t *testing.T) {
		f := newFixture(t)
		f.options.Explicit = "stored-token"
		f.store.put(record("stored-token", "stored-refresh-token", "2000-01-01T00:00:00Z"))
		if token, err := f.manager().Token(context.Background()); err != nil || token != "stored-token" {
			t.Errorf("Token() = %q, %v", token, err)
		}
		if len(f.api.calls) != 0 {
			t.Errorf("sent %v", f.api.calls)
		}
	})
}

// TestSessionOfAnotherProcess covers two processes that share a store. The
// one that comes second must not redeem a refresh token the first has
// spent, nor overwrite the session the first has stored.
func TestSessionOfAnotherProcess(t *testing.T) {
	other := record("other-token", "other-refresh-token", "")

	t.Run("is taken over after a 401", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "stored-refresh-token", ""))
		f.api.valid("other-token")
		m := f.manager()
		token, err := m.Token(context.Background())
		if err != nil || token != "stored-token" {
			t.Fatalf("Token() = %q, %v", token, err)
		}

		f.store.put(other)
		token, err = m.Rejected(context.Background(), token)
		if err != nil || token != "other-token" {
			t.Errorf("Rejected() = %q, %v; want the session of the other process", token, err)
		}
		if len(f.api.sent(refreshRoute)) != 0 {
			t.Errorf("redeemed %v: a refresh token that is spent", f.api.sent(refreshRoute))
		}
		if got := f.store.stored(); got != other {
			t.Errorf("stored %s, want the session of the other process", got)
		}
	})

	t.Run("is taken over after a refresh ahead of expiry lost the race", func(t *testing.T) {
		f := newFixture(t)
		f.store.put(record("stored-token", "stored-refresh-token", "2030-01-01T00:01:00Z"))
		f.api.on(refreshRoute, func(call) reply {
			f.store.put(other)
			return reply{status: 400}
		})
		token, err := f.manager().Token(context.Background())
		if err != nil || token != "other-token" {
			t.Errorf("Token() = %q, %v", token, err)
		}
	})

	t.Run("keeps the refresh token of the environment unspent", func(t *testing.T) {
		f := newFixture(t)
		f.export("env-token", "env-refresh-token")
		f.api.sessions["env-refresh-token"] = rotated
		f.api.valid("other-token")
		m := f.manager()
		token, err := m.Token(context.Background())
		if err != nil || token != "env-token" {
			t.Fatalf("Token() = %q, %v", token, err)
		}

		f.store.put(other)
		token, err = m.Rejected(context.Background(), token)
		if err != nil || token != "other-token" {
			t.Errorf("Rejected() = %q, %v", token, err)
		}
		if len(f.api.sent(refreshRoute)) != 0 || f.store.stored() != other {
			t.Errorf("refreshed %v and stored %s", f.api.sent(refreshRoute), f.store.stored())
		}
	})

	t.Run("gives way to the environment once it was rejected itself", func(t *testing.T) {
		f := newFixture(t)
		f.store.put("stored-token")
		f.export("env-token", "env-refresh-token")
		f.api.sessions["env-refresh-token"] = rotated

		token, source, err := f.manager().Resolve(context.Background(), false)
		if err != nil || token != "rotated-token" || source.Kind != auth.SourceRefresh {
			t.Errorf("Resolve() = %q, %v, %v", token, source, err)
		}
		if got := f.api.tokens(checkRoute); !slices.Equal(got, []string{"stored-token", "env-token"}) {
			t.Errorf("validated %v", got)
		}
	})
}
