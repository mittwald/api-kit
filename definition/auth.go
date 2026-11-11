package definition

import (
	"regexp"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/expr"
)

// Template variables of the auth section. Each has a value in some of its
// templates only; a definition that uses one elsewhere does not load.
const (
	// VariableToken is the token a request is sent with. The header, the
	// validation and the refresh know it.
	VariableToken = "token"
	// VariableRefreshToken is the refresh token of the session that is
	// being renewed. The refresh knows it.
	VariableRefreshToken = "refreshToken"
	// VariableCallbackURL is the address a browser login comes back to. The
	// URL of a browser login knows it.
	VariableCallbackURL = "callbackUrl"
)

// Defaults of the auth section.
const (
	defaultAuthHeader          = "Authorization"
	defaultAuthValue           = "Bearer ${" + VariableToken + "}"
	defaultLeewaySeconds       = 300
	defaultLoginTimeoutSeconds = 300
	defaultPasteLabel          = "Token"
	handoverPaste              = "paste"
)

var (
	// headerName is what RFC 9110 lets a header be called.
	headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	// loginName is what a login method may be called: it becomes part of
	// the name of a command.
	loginName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// Auth describes how requests are authenticated: the header that carries
// the token, where tokens come from, and the requests that check, renew and
// obtain them. A definition without an auth section has the header
// "Authorization: Bearer ${token}" and the store as its only source.
type Auth struct {
	// Header is the request header that carries the token.
	Header AuthHeader
	// Sources are the places a token is looked for, in order of preference.
	// A token given for the run itself is not listed: it goes before all of
	// them and switches them off.
	Sources []AuthSource
	// Validate asks the API whether a token is good. Nil means that it
	// cannot be asked: a token then counts as usable until a request fails.
	Validate *TokenValidation
	// Refresh renews a session with its refresh token. Nil means that
	// sessions cannot be renewed.
	Refresh *SessionRefresh
	// Login describes the ways to obtain a session interactively.
	Login Login
}

// AuthHeader is the request header that carries the token.
type AuthHeader struct {
	// Name is the name of the header.
	Name string
	// Value is the value of the header, a template of "${token}".
	Value string
}

// AuthSource is one place a token is looked for: the store, or an
// environment variable.
type AuthSource struct {
	// Store is true for the session a tool keeps between runs. A session
	// that is renewed, or obtained by a login, is written there whatever
	// its origin, so the store belongs before the environment: a variable
	// keeps its old token and would hide the renewed session on every run.
	// A definition that does not list the store keeps no session.
	Store bool
	// Env names the environment variable that holds a token.
	Env string
	// RefreshEnv names the environment variable that holds the refresh
	// token of the token in Env. Empty means that the token cannot be
	// renewed.
	RefreshEnv string
}

// AuthRequest is a request the authentication sends, or an address it
// builds.
type AuthRequest struct {
	// API names the API. It defaults to [Defaults.API].
	API string
	// Method is the HTTP method. It is empty for an address that is only
	// opened in a browser.
	Method string
	// Path is the path below the base URL of the API, a template.
	Path string
	// Query are the query parameters, in document order. Their values are
	// templates.
	Query []QueryParameter
	// Body is sent as JSON; its keys and strings are templates. Nil sends
	// no body.
	Body *apikit.Object
}

// templates returns everything in the request that is a template.
func (r AuthRequest) templates() []any {
	templates := []any{r.Path, r.Body}
	for _, parameter := range r.Query {
		templates = append(templates, parameter.Template)
	}
	return templates
}

// TokenValidation asks the API whether a token is good. The request carries
// the token in the header of [Auth], and knows it as "${token}".
type TokenValidation struct {
	// Request is the request to send.
	Request AuthRequest
	// Valid are the response statuses that prove the token good.
	Valid []int
	// Invalid are the response statuses that prove the token bad. Any
	// status in neither list proves nothing: the token is neither used nor
	// given up.
	Invalid []int
}

// SessionRefresh renews a session. The request carries the old token in the
// header of [Auth], and knows it as "${token}" and the refresh token as
// "${refreshToken}". A response with a status from 200 to 299 holds the new
// session.
type SessionRefresh struct {
	// Request is the request to send.
	Request AuthRequest
	// Rejected are the response statuses that say the session is gone for
	// good.
	Rejected []int
	// LeewaySeconds is how long before its expiry a stored session is
	// renewed, in place of waiting for the API to reject it.
	LeewaySeconds float64
	// Session says where the response holds the new session.
	Session SessionFields
}

// SessionFields say where a JSON object holds the parts of a session.
type SessionFields struct {
	// Token leads to the token. It defaults to the field "token".
	Token SessionField
	// RefreshToken leads to the refresh token. It defaults to the field
	// "refreshToken".
	RefreshToken SessionField
	// ExpiresAt leads to the time the token expires at. It defaults to the
	// field "expiresAt".
	ExpiresAt SessionField
}

// SessionField says where a JSON object holds one part of a session.
type SessionField struct {
	// Paths are tried in order; the first that leads to a string that is
	// not empty counts.
	Paths []expr.Path
	// Match cuts the value out of that string: the value is the first
	// match, and a string without one is a mistake. Nil takes the string as
	// it is.
	Match *regexp.Regexp
}

// Login describes the ways to obtain a session interactively.
type Login struct {
	// Fallback names the method a run falls back to when it has no token
	// and can ask the user. Empty means that such a run fails.
	Fallback string
	// Methods are the login methods, in document order.
	Methods []LoginMethod
}

// LoginKind says how a login method obtains a session.
type LoginKind string

// The kinds of login methods.
const (
	// LoginPassword asks questions and sends the answers to the API, which
	// responds with the session.
	LoginPassword LoginKind = "password"
	// LoginBrowser sends the user to a page in the browser and waits for
	// the browser to come back; the user then hands the session over.
	LoginBrowser LoginKind = "browser"
)

// LoginMethod is one way to obtain a session interactively. Which fields
// are set depends on Kind.
type LoginMethod struct {
	// Name selects the method. It starts with a lower-case letter and holds
	// lower-case letters, digits and dashes only.
	Name string
	// Kind says how the method works.
	Kind LoginKind
	// Prompts are the questions of a [LoginPassword] method, in the order
	// they are asked. The answers are template variables of Request, by the
	// names of the prompts.
	Prompts []Prompt
	// Request is sent by a [LoginPassword] method, without a token. A
	// response with a status from 200 to 299 holds the session.
	Request AuthRequest
	// URL is the page a [LoginBrowser] method opens. It knows the address
	// the browser has to come back to as "${callbackUrl}".
	URL AuthRequest
	// TimeoutSeconds is how long a [LoginBrowser] method waits for the
	// browser to come back.
	TimeoutSeconds float64
	// Submit is where the page that a [LoginBrowser] method ends on sends
	// the browser next, with an empty form, so that the browser shows the
	// response. Nil means that the page only says the login is complete.
	Submit *AuthRequest
	// Prompt asks for what the browser shows at the end of a [LoginBrowser]
	// method: a token, a JSON string that holds one, or a JSON object.
	Prompt Prompt
	// Session says where the session is held: in the response to Request,
	// or in the JSON object the user hands over.
	Session SessionFields
}

// Prompt is a question a login asks the user.
type Prompt struct {
	// Name is the template variable the answer becomes. It is empty for the
	// prompt of a [LoginBrowser] method.
	Name string
	// Label is the question.
	Label string
	// Secret hides the answer while it is typed.
	Secret bool
	// Hint is shown before the question, to say where the answer comes
	// from.
	Hint string
}

// requestKind says what an [AuthRequest] is for, which decides its keys.
type requestKind uint8

const (
	// requestSent is a request the tool sends: it has a method and a body.
	requestSent requestKind = iota
	// requestForm is a form a browser submits: it has a method.
	requestForm
	// requestLink is an address a browser opens.
	requestLink
)

// auth decodes the auth section. A definition without one gets the
// defaults.
func (l *loader) auth(m mapping, doc *Document) {
	doc.Auth = Auth{
		Header:  AuthHeader{Name: defaultAuthHeader, Value: defaultAuthValue},
		Sources: []AuthSource{{Store: true}},
	}
	auth, ok := m.child("auth")
	if !ok {
		return
	}
	if header, ok := auth.child("header"); ok {
		doc.Auth.Header = l.authHeader(header)
	}
	if auth.has("sources") {
		doc.Auth.Sources = l.authSources(auth)
	}
	if validate, ok := auth.child("validate"); ok {
		validation := &TokenValidation{
			Request: l.authRequest(validate, doc, requestSent, VariableToken),
			Valid:   validate.integers("valid", []int{200, 204}),
			Invalid: validate.integers("invalid", []int{401}),
		}
		for i, status := range validation.Invalid {
			if slices.Contains(validation.Valid, status) {
				l.problemf(index(validate.at("invalid"), i), "%d is a valid status as well", status)
			}
		}
		validate.finish()
		doc.Auth.Validate = validation
	}
	if refresh, ok := auth.child("refresh"); ok {
		doc.Auth.Refresh = &SessionRefresh{
			Request:       l.authRequest(refresh, doc, requestSent, VariableToken, VariableRefreshToken),
			Rejected:      refresh.integers("rejected", []int{400, 401, 403}),
			LeewaySeconds: refresh.number("leewaySeconds", defaultLeewaySeconds, 0),
			Session:       l.sessionFields(refresh),
		}
		refresh.finish()
	}
	if login, ok := auth.child("login"); ok {
		doc.Auth.Login = l.login(login, doc)
	}
	auth.finish()
}

func (l *loader) authHeader(m mapping) AuthHeader {
	header := AuthHeader{Name: m.required(keyName), Value: m.required("value")}
	if header.Name != "" && !headerName.MatchString(header.Name) {
		l.problemf(m.at(keyName), "%q is not the name of a header", header.Name)
	}
	if header.Value != "" && !slices.Contains(variablesOf(header.Value), VariableToken) {
		l.problemf(m.at("value"), "must hold ${%s}, the place of the token", VariableToken)
	}
	l.knownVariables(m.at("value"), header.Value, VariableToken)
	m.finish()
	return header
}

// authSources decodes the places a token is looked for: the word store, or
// a mapping that names an environment variable.
func (l *loader) authSources(m mapping) []AuthSource {
	var sources []AuthSource
	for i, item := range m.items("sources") {
		path := index(m.at("sources"), i)
		source := AuthSource{Store: true}
		if text, ok := item.(string); !ok || text != "store" {
			if ok {
				l.problemf(path, "must be store or a mapping with 'env', not %q", text)
				continue
			}
			entry, ok := l.mapping(path, item)
			if !ok {
				continue
			}
			source = AuthSource{Env: entry.required("env"), RefreshEnv: entry.text("refreshEnv", "")}
			entry.finish()
		}
		if slices.ContainsFunc(sources, func(other AuthSource) bool { return other.Store == source.Store && other.Env == source.Env }) {
			l.problemf(path, "is listed more than once")
			continue
		}
		sources = append(sources, source)
	}
	return sources
}

// authRequest decodes the keys of a request from m, which the caller
// finishes. variables are the template variables that have a value there.
func (l *loader) authRequest(m mapping, doc *Document, kind requestKind, variables ...string) AuthRequest {
	request := AuthRequest{API: m.text("api", doc.Defaults.API), Path: m.required("path")}
	if request.API != "" {
		l.knownAPI(m.at("api"), doc, request.API)
	}
	for _, pair := range m.pairs("query") {
		request.Query = append(request.Query, QueryParameter{Name: pair.Key, Template: pair.Value})
	}
	if kind != requestLink {
		request.Method = strings.ToUpper(m.text("method", defaultMethod))
	}
	switch kind {
	case requestSent:
		if body, ok := m.child("body"); ok {
			request.Body = body.object
		}
	case requestForm:
		if request.Method != "GET" && request.Method != "POST" {
			l.problemf(m.at("method"), "a browser submits a form with GET or POST, not %s", request.Method)
		}
	}
	l.knownVariables(m.path, request.templates(), variables...)
	return request
}

// knownVariables reports the variables of a template that have no value
// where it is used.
func (l *loader) knownVariables(path string, template any, known ...string) {
	for _, variable := range variablesOf(template) {
		switch {
		case slices.Contains(known, variable):
		case len(known) == 0:
			l.problemf(path, "template variable %q has no value: there are no variables here", variable)
		default:
			l.problemf(path, "template variable %q has no value, the variables here are: %s", variable, strings.Join(known, ", "))
		}
	}
}

// sessionFields decodes the session key of m: where a JSON object holds the
// parts of a session.
func (l *loader) sessionFields(m mapping) SessionFields {
	session, ok := m.child("session")
	if !ok {
		// Without the key every part is looked for under its own name.
		session, _ = l.mapping(m.at("session"), apikit.NewObject())
	}
	fields := SessionFields{
		Token:        l.sessionField(session, "token"),
		RefreshToken: l.sessionField(session, "refreshToken"),
		ExpiresAt:    l.sessionField(session, "expiresAt"),
	}
	session.finish()
	return fields
}

// sessionField decodes where one part of a session is held: a field, a list
// of fields, or a mapping of a field and the pattern that cuts the value
// out of it. An absent key means the field of the same name.
func (l *loader) sessionField(m mapping, key string) SessionField {
	path := m.at(key)
	value, ok := m.get(key)
	if !ok {
		value = key
	}
	var field SessionField
	var names []string
	switch value := value.(type) {
	case string:
		names = []string{value}
	case []any:
		names = l.texts(path, value)
		if len(value) == 0 {
			l.problemf(path, "needs at least one field")
		}
	case *apikit.Object:
		entry, _ := l.mapping(path, value)
		if name := entry.required("field"); name != "" {
			names = []string{name}
		}
		field.Match = entry.pattern("match")
		entry.finish()
	default:
		l.problemf(path, "must be a field, a list of fields, or a mapping with 'field' and 'match', not %s", kindOf(value))
	}
	for _, name := range names {
		parsed, err := expr.ParsePath(name)
		if err != nil {
			l.problemf(path, "%v", err)
			continue
		}
		field.Paths = append(field.Paths, parsed)
	}
	return field
}

func (l *loader) login(m mapping, doc *Document) Login {
	login := Login{Fallback: m.text("fallback", "")}
	methods, ok := m.child("methods")
	if ok {
		for name, value := range methods.object.All() {
			methods.used[name] = true
			if method, ok := l.loginMethod(methods.at(name), name, value, doc); ok {
				login.Methods = append(login.Methods, method)
			}
		}
	}
	if login.Fallback != "" && (!ok || !methods.has(login.Fallback)) {
		l.problemf(m.at("fallback"), "unknown login method %q", login.Fallback)
	}
	m.finish()
	return login
}

func (l *loader) loginMethod(path, name string, value any, doc *Document) (LoginMethod, bool) {
	m, ok := l.mapping(path, value)
	if !ok {
		return LoginMethod{}, false
	}
	if !loginName.MatchString(name) {
		l.problemf(path, "the name of a login method must start with a lower-case letter and hold only lower-case letters, digits and -")
	}
	method := LoginMethod{Name: name, Kind: LoginKind(m.required("kind"))}
	switch method.Kind {
	case LoginPassword:
		l.passwordLogin(m, &method, doc)
		l.otherKind(m, LoginBrowser, "url", "timeoutSeconds", "callback", "handover", "prompt")
	case LoginBrowser:
		l.browserLogin(m, &method, doc)
		l.otherKind(m, LoginPassword, "prompts", "request")
	case "":
		// Reported as missing. Which keys belong here is unknown without it.
		return LoginMethod{}, false
	default:
		l.problemf(m.at("kind"), "must be %s or %s, not %q", LoginPassword, LoginBrowser, string(method.Kind))
		return LoginMethod{}, false
	}
	method.Session = l.sessionFields(m)
	m.finish()
	return method, true
}

// otherKind reports the keys of a login method that only a method of
// another kind has.
func (l *loader) otherKind(m mapping, kind LoginKind, keys ...string) {
	for _, key := range keys {
		if m.has(key) {
			m.used[key] = true
			l.problemf(m.at(key), "belongs to a login of kind %s", kind)
		}
	}
}

func (l *loader) passwordLogin(m mapping, method *LoginMethod, doc *Document) {
	var names []string
	for i, item := range m.items("prompts") {
		entry, ok := l.mapping(index(m.at("prompts"), i), item)
		if !ok {
			continue
		}
		name := entry.required(keyName)
		if name != "" && slices.Contains(names, name) {
			l.problemf(entry.at(keyName), "%q is asked for more than once", name)
		}
		names = append(names, name)
		method.Prompts = append(method.Prompts, l.prompt(entry, Prompt{Name: name, Label: name}))
	}
	if len(method.Prompts) == 0 {
		l.problemf(m.at("prompts"), "needs at least one prompt")
	}
	if request, ok := m.child("request"); ok {
		method.Request = l.authRequest(request, doc, requestSent, names...)
		request.finish()
	} else if !m.has("request") {
		l.problemf(m.at("request"), "is required")
	}
}

func (l *loader) browserLogin(m mapping, method *LoginMethod, doc *Document) {
	if link, ok := m.child("url"); ok {
		method.URL = l.authRequest(link, doc, requestLink, VariableCallbackURL)
		if !slices.Contains(variablesOf(method.URL.templates()), VariableCallbackURL) {
			l.problemf(link.path, "must hold ${%s}, the address the browser comes back to", VariableCallbackURL)
		}
		link.finish()
	} else if !m.has("url") {
		l.problemf(m.at("url"), "is required")
	}
	method.TimeoutSeconds = m.number("timeoutSeconds", defaultLoginTimeoutSeconds, 1)
	if callback, ok := m.child("callback"); ok {
		if submit, ok := callback.child("submit"); ok {
			request := l.authRequest(submit, doc, requestForm)
			submit.finish()
			method.Submit = &request
		}
		callback.finish()
	}
	if handover := m.text("handover", handoverPaste); handover != handoverPaste {
		l.problemf(m.at("handover"), "must be %s, not %q", handoverPaste, handover)
	}
	method.Prompt = Prompt{Label: defaultPasteLabel, Secret: true}
	if prompt, ok := m.child("prompt"); ok {
		method.Prompt = l.prompt(prompt, method.Prompt)
	}
}

// prompt decodes the keys every prompt has and finishes m. def holds the
// name and the defaults.
func (l *loader) prompt(m mapping, def Prompt) Prompt {
	prompt := Prompt{
		Name:   def.Name,
		Label:  m.text("label", def.Label),
		Secret: m.flag("secret", def.Secret),
		Hint:   m.text("hint", ""),
	}
	m.finish()
	return prompt
}
