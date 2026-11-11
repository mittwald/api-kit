package definition_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mittwald/api-kit/definition"
)

// withAuth returns a definition with the given auth section.
func withAuth(auth string) string {
	var b strings.Builder
	b.WriteString(environments)
	b.WriteString("auth:\n")
	for line := range strings.SplitSeq(strings.Trim(auth, "\n"), "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// pathsOf returns the paths of a session field as written.
func pathsOf(field definition.SessionField) []string {
	paths := make([]string, len(field.Paths))
	for i, path := range field.Paths {
		paths[i] = path.String()
	}
	return paths
}

func TestAuthDefaults(t *testing.T) {
	for name, doc := range map[string]string{
		"no auth section":    environments,
		"empty auth section": environments + "auth: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			auth := mustLoad(t, doc).Auth
			if want := (definition.AuthHeader{Name: "Authorization", Value: "Bearer ${token}"}); auth.Header != want {
				t.Errorf("Header = %+v, want %+v", auth.Header, want)
			}
			if !slices.Equal(auth.Sources, []definition.AuthSource{{Store: true}}) {
				t.Errorf("Sources = %+v, want the store alone", auth.Sources)
			}
			if auth.Validate != nil || auth.Refresh != nil || auth.Login.Fallback != "" || len(auth.Login.Methods) != 0 {
				t.Errorf("Auth = %+v, want no validation, no refresh and no login", auth)
			}
		})
	}
}

func TestAuthSectionDefaults(t *testing.T) {
	auth := mustLoad(t, withAuth(`
sources: []
validate: {path: tokens/check}
refresh: {path: sessions}
login:
  methods:
    password:
      kind: password
      prompts: [{name: username}]
      request: {path: login}
    sso:
      kind: browser
      url: {path: "sso/${callbackUrl}"}
`)).Auth

	if len(auth.Sources) != 0 {
		t.Errorf("Sources = %+v, want none for an empty list", auth.Sources)
	}
	validate := auth.Validate
	if validate.Request.API != "main" || validate.Request.Method != "POST" || validate.Request.Body != nil ||
		!slices.Equal(validate.Valid, []int{200, 204}) || !slices.Equal(validate.Invalid, []int{401}) {
		t.Errorf("Validate = %+v", validate)
	}
	refresh := auth.Refresh
	if refresh.Request.Method != "POST" || !slices.Equal(refresh.Rejected, []int{400, 401, 403}) || refresh.LeewaySeconds != 300 {
		t.Errorf("Refresh = %+v", refresh)
	}
	for name, field := range map[string]definition.SessionField{
		"token":        refresh.Session.Token,
		"refreshToken": refresh.Session.RefreshToken,
		"expiresAt":    refresh.Session.ExpiresAt,
	} {
		if !slices.Equal(pathsOf(field), []string{name}) || field.Match != nil {
			t.Errorf("session field %s = %v, want the field of that name", name, pathsOf(field))
		}
	}

	password, sso := auth.Login.Methods[0], auth.Login.Methods[1]
	if want := (definition.Prompt{Name: "username", Label: "username"}); password.Prompts[0] != want {
		t.Errorf("prompt = %+v, want %+v", password.Prompts[0], want)
	}
	if !slices.Equal(pathsOf(password.Session.Token), []string{"token"}) {
		t.Errorf("session of the password login = %+v", password.Session)
	}
	if want := (definition.Prompt{Label: "Token", Secret: true}); sso.Prompt != want {
		t.Errorf("prompt of the browser login = %+v, want %+v", sso.Prompt, want)
	}
	if sso.TimeoutSeconds != 300 || sso.Submit != nil || sso.URL.Method != "" {
		t.Errorf("browser login = %+v", sso)
	}
}

// authFull exercises every key of the auth section once.
const authFull = `
environments:
  production:
    apis:
      main: https://api.example.test/v1
      accounts: https://accounts.example.test
defaults: {api: main}
auth:
  header: {name: X-Token, value: "${token}"}
  sources:
    - store
    - {env: EXAMPLE_TOKEN, refreshEnv: EXAMPLE_REFRESH_TOKEN}
    - {env: EXAMPLE_CI_TOKEN}
  validate:
    api: accounts
    method: post
    path: tokens/check
    query: {scope: "${token}"}
    body: {}
    valid: [200]
    invalid: [401, 410]
  refresh:
    api: accounts
    method: put
    path: sessions/${token}
    body: {refreshToken: "${refreshToken}"}
    rejected: [400, 404]
    leewaySeconds: 60.5
    session: {token: token, refreshToken: [refreshToken, refresh], expiresAt: validity.until}
  login:
    fallback: password
    methods:
      password:
        kind: password
        prompts:
          - {name: username, label: Username, hint: The address you signed up with.}
          - {name: password, label: Password, secret: true}
        request: {api: accounts, method: POST, path: login, body: {email: "${username}", password: "${password}"}}
        session: {token: {field: token, match: "^[^:]+:[^:]+"}, refreshToken: refreshToken, expiresAt: expires}
      sso-2:
        kind: browser
        url: {api: accounts, path: sso/start, query: {redirectUri: "${callbackUrl}", prompt: login}}
        timeoutSeconds: 120
        callback: {submit: {api: accounts, method: get, path: tokens/show, query: {format: json}}}
        handover: paste
        prompt: {label: "Paste the token", secret: false, hint: "Copy the token from the browser response."}
        session: {token: [accessToken, token], refreshToken: refreshToken, expiresAt: [expiresAt, expires]}
`

func TestAuthDecodesEveryKey(t *testing.T) {
	auth := mustLoad(t, authFull).Auth

	if want := (definition.AuthHeader{Name: "X-Token", Value: "${token}"}); auth.Header != want {
		t.Errorf("Header = %+v, want %+v", auth.Header, want)
	}
	if want := []definition.AuthSource{
		{Store: true},
		{Env: "EXAMPLE_TOKEN", RefreshEnv: "EXAMPLE_REFRESH_TOKEN"},
		{Env: "EXAMPLE_CI_TOKEN"},
	}; !slices.Equal(auth.Sources, want) {
		t.Errorf("Sources = %+v, want %+v", auth.Sources, want)
	}

	validate := auth.Validate
	if validate.Request.API != "accounts" || validate.Request.Method != "POST" || validate.Request.Path != "tokens/check" ||
		!slices.Equal(validate.Request.Query, []definition.QueryParameter{{Name: "scope", Template: "${token}"}}) ||
		validate.Request.Body == nil || validate.Request.Body.Len() != 0 ||
		!slices.Equal(validate.Valid, []int{200}) || !slices.Equal(validate.Invalid, []int{401, 410}) {
		t.Errorf("Validate = %+v", validate)
	}

	refresh := auth.Refresh
	if refresh.Request.API != "accounts" || refresh.Request.Method != "PUT" || refresh.Request.Path != "sessions/${token}" ||
		!slices.Equal(refresh.Request.Body.Keys(), []string{"refreshToken"}) ||
		!slices.Equal(refresh.Rejected, []int{400, 404}) || refresh.LeewaySeconds != 60.5 {
		t.Errorf("Refresh = %+v", refresh)
	}
	if !slices.Equal(pathsOf(refresh.Session.Token), []string{"token"}) ||
		!slices.Equal(pathsOf(refresh.Session.RefreshToken), []string{"refreshToken", "refresh"}) ||
		!slices.Equal(pathsOf(refresh.Session.ExpiresAt), []string{"validity.until"}) {
		t.Errorf("Refresh.Session = %+v", refresh.Session)
	}

	if auth.Login.Fallback != "password" || len(auth.Login.Methods) != 2 {
		t.Fatalf("Login = %+v", auth.Login)
	}
	password, sso := auth.Login.Methods[0], auth.Login.Methods[1]
	if password.Name != "password" || password.Kind != definition.LoginPassword ||
		!slices.Equal(password.Prompts, []definition.Prompt{
			{Name: "username", Label: "Username", Hint: "The address you signed up with."},
			{Name: "password", Label: "Password", Secret: true},
		}) ||
		password.Request.API != "accounts" || password.Request.Method != "POST" || password.Request.Path != "login" ||
		!slices.Equal(password.Request.Body.Keys(), []string{"email", "password"}) {
		t.Errorf("password login = %+v", password)
	}
	if token := password.Session.Token; !slices.Equal(pathsOf(token), []string{"token"}) || token.Match.FindString("abc:def:ghi") != "abc:def" ||
		!slices.Equal(pathsOf(password.Session.ExpiresAt), []string{"expires"}) {
		t.Errorf("session of the password login = %+v", password.Session)
	}

	if sso.Name != "sso-2" || sso.Kind != definition.LoginBrowser || sso.TimeoutSeconds != 120 ||
		sso.URL.API != "accounts" || sso.URL.Path != "sso/start" || sso.URL.Method != "" ||
		!slices.Equal(sso.URL.Query, []definition.QueryParameter{{Name: "redirectUri", Template: "${callbackUrl}"}, {Name: "prompt", Template: "login"}}) {
		t.Errorf("browser login = %+v", sso)
	}
	if submit := sso.Submit; submit == nil || submit.API != "accounts" || submit.Method != "GET" || submit.Path != "tokens/show" ||
		!slices.Equal(submit.Query, []definition.QueryParameter{{Name: "format", Template: "json"}}) {
		t.Errorf("Submit = %+v", sso.Submit)
	}
	if want := (definition.Prompt{Label: "Paste the token", Hint: "Copy the token from the browser response."}); sso.Prompt != want {
		t.Errorf("prompt of the browser login = %+v, want %+v", sso.Prompt, want)
	}
	if !slices.Equal(pathsOf(sso.Session.Token), []string{"accessToken", "token"}) ||
		!slices.Equal(pathsOf(sso.Session.ExpiresAt), []string{"expiresAt", "expires"}) {
		t.Errorf("session of the browser login = %+v", sso.Session)
	}
}

func TestAuthMergesOverlays(t *testing.T) {
	doc := mustLoad(t,
		withAuth("sources: [store]\nlogin:\n  methods:\n    password: {kind: password, prompts: [{name: username}], request: {path: login}}\n"),
		"auth:\n  sources: [{env: EXAMPLE_TOKEN}]\n  login:\n    fallback: password\n")

	if want := []definition.AuthSource{{Store: true}, {Env: "EXAMPLE_TOKEN"}}; !slices.Equal(doc.Auth.Sources, want) {
		t.Errorf("Sources = %+v, want %+v", doc.Auth.Sources, want)
	}
	if doc.Auth.Login.Fallback != "password" {
		t.Errorf("Fallback = %q, want the one of the overlay", doc.Auth.Login.Fallback)
	}
}

func TestAuthProblems(t *testing.T) {
	const typed = "login:\n  methods:\n    password:\n      kind: password\n"
	const browser = "login:\n  methods:\n    sso:\n      kind: browser\n"
	const url = "      url: {path: sso, query: {to: \"${callbackUrl}\"}}\n"
	const asked = "      prompts: [{name: username}]\n      request: {path: login}\n"
	tests := []struct {
		name string
		auth string
		want []string
	}{
		{"section that is no mapping", "[store]",
			[]string{"auth: must be a mapping, not a list"}},
		{"unknown key", "tokens: [store]",
			[]string{"auth.tokens: unknown key"}},

		{"header without its keys", "header: {}",
			[]string{"auth.header.name: is required", "auth.header.value: is required"}},
		{"header name that is none", `header: {name: "X Token", value: "${token}"}`,
			[]string{`auth.header.name: "X Token" is not the name of a header`}},
		{"header value without the token", "header: {name: X-Token, value: static}",
			[]string{"auth.header.value: must hold ${token}, the place of the token"}},
		{"header value with a plain placeholder", "header: {name: X-Token, value: $token}",
			[]string{"auth.header.value: must hold ${token}, the place of the token"}},
		{"header value with an unknown variable", `header: {name: X-Token, value: "${token} ${refreshToken}"}`,
			[]string{`auth.header.value: template variable "refreshToken" has no value, the variables here are: token`}},
		{"unknown key of the header", `header: {name: X-Token, value: "${token}", prefix: Bearer}`,
			[]string{"auth.header.prefix: unknown key"}},

		{"sources that are no list", "sources: store",
			[]string{"auth.sources: must be a list, not a string"}},
		{"source of an unknown kind", "sources: [keyring]",
			[]string{`auth.sources[0]: must be store or a mapping with 'env', not "keyring"`}},
		{"source that is a number", "sources: [1]",
			[]string{"auth.sources[0]: must be a mapping, not a number"}},
		{"source without a variable", "sources: [{refreshEnv: EXAMPLE_REFRESH_TOKEN}]",
			[]string{"auth.sources[0].env: is required"}},
		{"store listed twice", "sources: [store, store]",
			[]string{"auth.sources[1]: is listed more than once"}},
		{"variable listed twice", "sources: [{env: EXAMPLE_TOKEN}, {env: EXAMPLE_TOKEN, refreshEnv: EXAMPLE_REFRESH_TOKEN}]",
			[]string{"auth.sources[1]: is listed more than once"}},
		{"unknown key of a source", "sources: [{env: EXAMPLE_TOKEN, prefix: x}]",
			[]string{"auth.sources[0].prefix: unknown key"}},

		{"validation without a path", "validate: {method: POST}",
			[]string{"auth.validate.path: is required"}},
		{"validation on an unknown API", "validate: {api: other, path: tokens/check}",
			[]string{`auth.validate.api: unknown API "other"`}},
		{"validation with an unknown variable", `validate: {path: tokens/check, body: {token: "${refreshToken}"}}`,
			[]string{`auth.validate: template variable "refreshToken" has no value, the variables here are: token`}},
		{"validation with an unknown variable in the query", `validate: {path: tokens/check, query: {id: "${session}"}}`,
			[]string{`auth.validate: template variable "session" has no value`}},
		{"status that is valid and invalid", "validate: {path: tokens/check, valid: [200, 401], invalid: [403, 401]}",
			[]string{"auth.validate.invalid[1]: 401 is a valid status as well"}},
		{"statuses that are no numbers", "validate: {path: tokens/check, valid: [ok]}",
			[]string{"auth.validate.valid[0]: must be a whole number"}},
		{"body that is no mapping", "validate: {path: tokens/check, body: token}",
			[]string{"auth.validate.body: must be a mapping, not a string"}},
		{"unknown key of the validation", "validate: {path: tokens/check, rejected: [401]}",
			[]string{"auth.validate.rejected: unknown key"}},

		{"refresh with an unknown variable", `refresh: {path: "sessions/${session}"}`,
			[]string{`auth.refresh: template variable "session" has no value, the variables here are: token, refreshToken`}},
		{"negative leeway", "refresh: {path: sessions, leewaySeconds: -1}",
			[]string{"auth.refresh.leewaySeconds: must be a number of at least 0"}},
		{"session that is no mapping", "refresh: {path: sessions, session: token}",
			[]string{"auth.refresh.session: must be a mapping, not a string"}},
		{"unknown part of a session", "refresh: {path: sessions, session: {accessToken: token}}",
			[]string{"auth.refresh.session.accessToken: unknown key"}},
		{"session field that is a number", "refresh: {path: sessions, session: {token: 1}}",
			[]string{"auth.refresh.session.token: must be a field, a list of fields, or a mapping with 'field' and 'match', not a number"}},
		{"session field that is no path", `refresh: {path: sessions, session: {token: "data..token"}}`,
			[]string{`auth.refresh.session.token: path "data..token": empty segment`}},
		{"session field that is empty", `refresh: {path: sessions, session: {token: ""}}`,
			[]string{"auth.refresh.session.token: empty path"}},
		{"empty list of session fields", "refresh: {path: sessions, session: {token: []}}",
			[]string{"auth.refresh.session.token: needs at least one field"}},
		{"session fields that are no strings", "refresh: {path: sessions, session: {token: [1]}}",
			[]string{"auth.refresh.session.token[0]: must be a string, not a number"}},
		{"session field without its field", `refresh: {path: sessions, session: {token: {match: "^a"}}}`,
			[]string{"auth.refresh.session.token.field: is required"}},
		{"session field with a bad pattern", `refresh: {path: sessions, session: {token: {field: token, match: "("}}}`,
			[]string{`auth.refresh.session.token.match: "(" is not a regular expression`}},
		{"unknown key of a session field", "refresh: {path: sessions, session: {token: {field: token, first: true}}}",
			[]string{"auth.refresh.session.token.first: unknown key"}},

		{"fallback that is no method", "login: {fallback: password}",
			[]string{`auth.login.fallback: unknown login method "password"`}},
		{"fallback that is another method", "login:\n  fallback: sso\n  methods:\n    password:\n      kind: password\n" + asked,
			[]string{`auth.login.fallback: unknown login method "sso"`}},
		{"methods that are a list", "login: {methods: [password]}",
			[]string{"auth.login.methods: must be a mapping, not a list"}},
		{"method that is no mapping", "login: {methods: {password: true}}",
			[]string{"auth.login.methods.password: must be a mapping, not a boolean"}},
		{"method name that is no command name", "login:\n  methods:\n    Single_SignOn:\n      kind: password\n" + asked,
			[]string{"auth.login.methods.Single_SignOn: the name of a login method must start with a lower-case letter"}},
		{"method without a kind", "login:\n  methods:\n    password:\n" + asked,
			[]string{"auth.login.methods.password.kind: is required"}},
		{"method of an unknown kind", "login: {methods: {token: {kind: device}}}",
			[]string{`auth.login.methods.token.kind: must be password or browser, not "device"`}},
		{"unknown key of the login", "login: {default: password}",
			[]string{"auth.login.default: unknown key"}},

		{"password login without its keys", typed,
			[]string{"auth.login.methods.password.prompts: needs at least one prompt", "auth.login.methods.password.request: is required"}},
		{"password login with the keys of a browser login", typed + asked + url + "      timeoutSeconds: 5\n      callback: {}\n      handover: paste\n      prompt: {}\n",
			[]string{
				"auth.login.methods.password.url: belongs to a login of kind browser",
				"auth.login.methods.password.timeoutSeconds: belongs to a login of kind browser",
				"auth.login.methods.password.callback: belongs to a login of kind browser",
				"auth.login.methods.password.handover: belongs to a login of kind browser",
				"auth.login.methods.password.prompt: belongs to a login of kind browser",
			}},
		{"prompt without a name", typed + "      prompts: [{label: Username}]\n      request: {path: login}\n",
			[]string{"auth.login.methods.password.prompts[0].name: is required"}},
		{"prompt asked twice", typed + "      prompts: [{name: username}, {name: username}]\n      request: {path: login}\n",
			[]string{`auth.login.methods.password.prompts[1].name: "username" is asked for more than once`}},
		{"prompt that is no mapping", typed + "      prompts: [username]\n      request: {path: login}\n",
			[]string{"auth.login.methods.password.prompts[0]: must be a mapping, not a string", "auth.login.methods.password.prompts: needs at least one prompt"}},
		{"unknown key of a prompt", typed + "      prompts: [{name: username, hidden: true}]\n      request: {path: login}\n",
			[]string{"auth.login.methods.password.prompts[0].hidden: unknown key"}},
		{"login request with an unknown variable", typed + "      prompts: [{name: username}]\n      request: {path: login, body: {user: \"${username}\", token: \"${token}\"}}\n",
			[]string{`auth.login.methods.password.request: template variable "token" has no value, the variables here are: username`}},
		{"login request on an unknown API", typed + "      prompts: [{name: username}]\n      request: {api: other, path: login}\n",
			[]string{`auth.login.methods.password.request.api: unknown API "other"`}},
		{"unknown key of a password login", typed + asked + "      scope: all\n",
			[]string{"auth.login.methods.password.scope: unknown key"}},

		{"browser login without a URL", browser,
			[]string{"auth.login.methods.sso.url: is required"}},
		{"browser login with the keys of a password login", browser + url + asked,
			[]string{
				"auth.login.methods.sso.prompts: belongs to a login of kind password",
				"auth.login.methods.sso.request: belongs to a login of kind password",
			}},
		{"URL without the address to come back to", browser + "      url: {path: sso}\n",
			[]string{"auth.login.methods.sso.url: must hold ${callbackUrl}, the address the browser comes back to"}},
		{"URL with an unknown variable", browser + "      url: {path: sso, query: {to: \"${callbackUrl}\", as: \"${token}\"}}\n",
			[]string{`auth.login.methods.sso.url: template variable "token" has no value, the variables here are: callbackUrl`}},
		{"URL with a method", browser + "      url: {method: GET, path: \"sso/${callbackUrl}\"}\n",
			[]string{"auth.login.methods.sso.url.method: unknown key"}},
		{"timeout below a second", browser + url + "      timeoutSeconds: 0\n",
			[]string{"auth.login.methods.sso.timeoutSeconds: must be a number of at least 1"}},
		{"unknown way to hand the session over", browser + url + "      handover: redirect\n",
			[]string{`auth.login.methods.sso.handover: must be paste, not "redirect"`}},
		{"form sent with another method", browser + url + "      callback: {submit: {method: PUT, path: tokens/check}}\n",
			[]string{"auth.login.methods.sso.callback.submit.method: a browser submits a form with GET or POST, not PUT"}},
		{"form with a variable", browser + url + "      callback: {submit: {path: \"tokens/${token}\"}}\n",
			[]string{`auth.login.methods.sso.callback.submit: template variable "token" has no value: there are no variables here`}},
		{"form with a body", browser + url + "      callback: {submit: {path: tokens/check, body: {}}}\n",
			[]string{"auth.login.methods.sso.callback.submit.body: unknown key"}},
		{"unknown key of the callback", browser + url + "      callback: {page: done}\n",
			[]string{"auth.login.methods.sso.callback.page: unknown key"}},
		{"named prompt of a browser login", browser + url + "      prompt: {name: token}\n",
			[]string{"auth.login.methods.sso.prompt.name: unknown key"}},
		{"secret that is no boolean", browser + url + "      prompt: {secret: \"yes\"}\n",
			[]string{"auth.login.methods.sso.prompt.secret: must be true or false, not a string"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := load(t, withAuth(tt.auth))
			wantProblems(t, err, tt.want...)
		})
	}
}
