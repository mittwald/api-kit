package auth_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/definition"
)

func TestSessionsAreKeptPerAccount(t *testing.T) {
	f := newFixture(t)
	f.options.Account = "staging"
	f.store.put(record("stored-token", "", ""))
	f.store.values["staging"] = record("staging-token", "staging-refresh-token", "")
	f.api.sessions["staging-refresh-token"] = rotated
	m := f.manager()

	if token, err := m.Token(context.Background()); err != nil || token != "staging-token" {
		t.Fatalf("Token() = %q, %v", token, err)
	}
	if token, err := m.Rejected(context.Background(), "staging-token"); err != nil || token != "rotated-token" {
		t.Fatalf("Rejected() = %q, %v", token, err)
	}
	if got := f.store.values["staging"]; got != record("rotated-token", "rotated-refresh-token", "2030-01-02T00:00:00Z") {
		t.Errorf("stored %s for the account", got)
	}
	if got := f.store.stored(); got != record("stored-token", "", "") {
		t.Errorf("the session of another account became %s", got)
	}
}

func TestStoredValueThatCannotBeRead(t *testing.T) {
	for name, value := range map[string]string{
		"an object of another shape": `{"token": 5}`,
		"an object that is cut off":  `{"token": "stored-token"`,
		"an empty value":             "",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.store.put(value)
			f.env["EXAMPLE_TOKEN"] = "env-token"
			if token, err := f.manager().Token(context.Background()); err != nil || token != "env-token" {
				t.Errorf("Token() = %q, %v; want the next source", token, err)
			}
			if warned := strings.Contains(f.log.String(), "cannot read the stored session"); warned != (value != "") {
				t.Errorf("warned about the store: %v\n%s", warned, f.log)
			}
		})
	}
}

// TestBrokenStore covers a store that does not work, as opposed to one
// that holds nothing: the run goes on without it and says so in the log.
func TestBrokenStore(t *testing.T) {
	f := newFixture(t)
	f.store.broken = errors.New("the keyring is locked")
	f.export("env-token", "env-refresh-token")
	f.api.sessions["env-refresh-token"] = rotated
	m := f.manager()

	if token, err := m.Token(context.Background()); err != nil || token != "env-token" {
		t.Fatalf("Token() = %q, %v", token, err)
	}
	if !strings.Contains(f.log.String(), "level=WARN") || !strings.Contains(f.log.String(), "the keyring is locked") {
		t.Errorf("the log does not warn about the store:\n%s", f.log)
	}
	// A session that cannot be stored still serves this process.
	if token, err := m.Rejected(context.Background(), "env-token"); err != nil || token != "rotated-token" {
		t.Errorf("Rejected() = %q, %v", token, err)
	}
	if !strings.Contains(f.log.String(), "cannot store the refreshed session") {
		t.Errorf("the log does not warn that the session is not stored:\n%s", f.log)
	}

	existed, err := m.Logout()
	if err == nil || existed || !strings.Contains(err.Error(), "the keyring is locked") {
		t.Errorf("Logout() = %v, %v; want the error of the store", existed, err)
	}
}

func TestAbsentSessionIsNoWarning(t *testing.T) {
	f := newFixture(t)
	f.env["EXAMPLE_TOKEN"] = "env-token"
	if _, err := f.manager().Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.log.String(), "level=WARN") {
		t.Errorf("an empty store was logged as a warning:\n%s", f.log)
	}
}

func TestLogout(t *testing.T) {
	f := newFixture(t)
	f.store.put(record("stored-token", "stored-refresh-token", ""))
	f.store.values["staging"] = "staging-token"
	m := f.manager()
	if token, err := m.Token(context.Background()); err != nil || token != "stored-token" {
		t.Fatalf("Token() = %q, %v", token, err)
	}
	m.Confirmed("stored-token")

	existed, err := m.Logout()
	if err != nil || !existed {
		t.Errorf("Logout() = %v, %v; want a session removed", existed, err)
	}
	if _, ok := f.store.values[account]; ok || f.store.values["staging"] != "staging-token" {
		t.Errorf("the store holds %v, want the session of the account gone and the other kept", f.store.values)
	}
	// The process forgets the token as well.
	_, err = m.Token(context.Background())
	wantUnauthenticated(t, err, true, false, false)

	existed, err = m.Logout()
	if err != nil || existed {
		t.Errorf("second Logout() = %v, %v; want nothing to remove", existed, err)
	}
}

func TestWithoutAStoreNothingIsRemembered(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fixture)
	}{
		{"no store", func(f *fixture) { f.options.Store = nil }},
		{"a definition that does not list the store", func(f *fixture) {
			f.options.Config.Sources = slices.DeleteFunc(f.options.Config.Sources, func(source definition.AuthSource) bool { return source.Store })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.store.put("stored-token")
			f.export("env-token", "env-refresh-token")
			f.api.sessions["env-refresh-token"] = rotated
			tt.change(f)
			m := f.manager()

			if token, err := m.Token(context.Background()); err != nil || token != "env-token" {
				t.Fatalf("Token() = %q, %v", token, err)
			}
			// The session is renewed for this process and stored nowhere.
			if token, err := m.Rejected(context.Background(), "env-token"); err != nil || token != "rotated-token" {
				t.Errorf("Rejected() = %q, %v", token, err)
			}
			if existed, err := m.Logout(); err != nil || existed {
				t.Errorf("Logout() = %v, %v", existed, err)
			}
			if f.store.stored() != "stored-token" || f.store.reads != 0 {
				t.Errorf("the store was used: it holds %q after %d reads", f.store.stored(), f.store.reads)
			}
		})
	}
}

func TestMethodsListsTheLoginsOfTheDefinition(t *testing.T) {
	f := newFixture(t)
	methods := f.manager().Methods()
	if len(methods) != 2 || methods[0].Name != "password" || methods[0].Kind != definition.LoginPassword ||
		methods[1].Name != "sso" || methods[1].Kind != definition.LoginBrowser {
		t.Errorf("Methods() = %+v", methods)
	}
	var none auth.Source
	if none.Kind != 0 {
		t.Errorf("the zero Source has kind %d", none.Kind)
	}
}
