package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

const (
	// headerTimeout is how long the browser may take to send a request to
	// the address a login comes back to.
	headerTimeout = 10 * time.Second
	// shutdownGrace is how long the page a browser login ends on may take
	// to reach the browser before the connection is cut.
	shutdownGrace = 5 * time.Second
	// excerptLength is the most that an error shows of a response.
	excerptLength = 300
)

// Login runs a login method of the definition and stores the session it
// yields; an empty method means the fallback method. The method needs a
// [UI], and a browser login an interactive one.
//
// The new token is put to the API before it is stored. A token the API
// rejects is not stored. A token the API gives no verdict on is stored with
// a notice, as is a session that cannot be renewed later.
func (m *Manager) Login(ctx context.Context, method string) (Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	chosen, ok := m.method(method)
	switch {
	case !ok && method == "":
		return Source{}, apikit.Errorf(apikit.KindUsage, "the definition names no login method to fall back to")
	case !ok:
		return Source{}, apikit.Errorf(apikit.KindUsage, "unknown login method %q", method)
	case m.o.UI == nil:
		return Source{}, apikit.Errorf(apikit.KindUnauthenticated, "login %q needs an interactive terminal", chosen.Name)
	}
	if _, err := m.login(ctx, chosen); err != nil {
		return Source{}, err
	}
	return Source{Kind: SourceLogin, Name: chosen.Name}, nil
}

// method returns the login method of that name, or the fallback method for
// an empty name.
func (m *Manager) method(name string) (definition.LoginMethod, bool) {
	if name == "" {
		name = m.o.Config.Login.Fallback
	}
	for _, method := range m.o.Config.Login.Methods {
		if method.Name == name {
			return method, true
		}
	}
	return definition.LoginMethod{}, false
}

// login runs a login method, puts the token it yields to the API and keeps
// the session. It returns the token.
func (m *Manager) login(ctx context.Context, method definition.LoginMethod) (string, error) {
	var fresh session
	var err error
	if method.Kind == definition.LoginBrowser {
		fresh, err = m.browserLogin(ctx, method)
	} else {
		fresh, err = m.passwordLogin(ctx, method)
	}
	if err != nil {
		return "", err
	}

	// The token is stored only once the API has had its say: a token that
	// is stored first and rejected then would be offered on every run.
	outcome := m.validate(ctx, fresh.Token)
	switch outcome {
	case verdictInvalid:
		return "", apikit.Errorf(apikit.KindUnauthenticated, "the API rejected the token of login %q", method.Name)
	case verdictUnknown:
		m.o.UI.Notify("Token validation is unavailable; the new API token is kept unvalidated.")
	}
	stored, err := m.save(fresh)
	if err != nil {
		return "", fmt.Errorf("cannot store the session: %w", err)
	}
	if m.o.Store != nil && m.o.Config.Refresh != nil && stored.RefreshToken == "" {
		m.o.UI.Notify("The login yielded no refresh token, so the session cannot be renewed and needs another login once it expires.")
	}
	delete(m.rejected, fresh.Token)
	if m.o.Explicit == "" {
		m.token, m.confirmed = fresh.Token, outcome == verdictValid
	}
	return fresh.Token, nil
}

// passwordLogin asks the questions of the method and sends the answers to
// the API, whose response holds the session.
func (m *Manager) passwordLogin(ctx context.Context, method definition.LoginMethod) (session, error) {
	answers := map[string]string{}
	var secrets []string
	for _, prompt := range method.Prompts {
		answer, err := m.o.UI.Ask(ctx, prompt)
		if err != nil {
			return session{}, err
		}
		answer = strings.TrimSpace(answer)
		answers[prompt.Name] = answer
		if prompt.Secret && answer != "" {
			secrets = append(secrets, answer)
		}
	}
	status, body, err := m.send(ctx, method.Request, answers, "")
	if err != nil {
		return session{}, fmt.Errorf("login %q failed: %w", method.Name, err)
	}
	if status < 200 || status > 299 {
		return session{}, apikit.Errorf(apikit.KindUnauthenticated, "login %q failed: the API answered %d%s", method.Name, status, excerpt(body, secrets))
	}
	fresh, err := sessionFromJSON(method.Session, body)
	if err != nil {
		return session{}, apikit.Errorf(apikit.KindUnauthenticated, "login %q failed: %w", method.Name, err)
	}
	return fresh, nil
}

// excerpt returns the start of a response for an error message, on one
// line and after a colon, or nothing for an empty response. The secrets,
// which a response may repeat, are blanked, as they are and as JSON spells
// them.
func excerpt(body []byte, secrets []string) string {
	text := string(body)
	for _, secret := range secrets {
		text = strings.ReplaceAll(text, secret, "***")
		if quoted, err := apikit.Marshal(secret); err == nil && len(quoted) > 2 {
			text = strings.ReplaceAll(text, string(quoted[1:len(quoted)-1]), "***")
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	if len(text) > excerptLength {
		cut := excerptLength
		for !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return ": " + text
}

// browserLogin sends the user through the login page of the method and
// asks for what the browser shows in the end.
func (m *Manager) browserLogin(ctx context.Context, method definition.LoginMethod) (session, error) {
	if !m.o.UI.Interactive() {
		return session{}, apikit.Errorf(apikit.KindUnauthenticated, "login %q needs an interactive terminal", method.Name)
	}
	if err := m.awaitBrowser(ctx, method); err != nil {
		return session{}, err
	}
	answer, err := m.o.UI.Ask(ctx, method.Prompt)
	if err != nil {
		return session{}, err
	}
	fresh, err := pasted(method.Session, answer)
	if err != nil {
		return session{}, apikit.Errorf(apikit.KindUnauthenticated, "login %q failed: %w", method.Name, err)
	}
	if fresh.Token == "" {
		return session{}, apikit.Errorf(apikit.KindUnauthenticated, "login %q yielded no token", method.Name)
	}
	return fresh, nil
}

// awaitBrowser opens the login page of the method and returns when the
// browser has come back from it.
//
// Where the browser comes back to is a server on the loopback interface,
// at a port of the system's choice and a path that cannot be guessed, so
// that no other page can end the wait. The server learns that the login is
// done and nothing else: the session never passes through it.
func (m *Manager) awaitBrowser(ctx context.Context, method definition.LoginMethod) error {
	var listen net.ListenConfig
	listener, err := listen.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("login %q cannot listen for the browser: %w", method.Name, err)
	}
	path := "/callback/" + rand.Text()
	loginURL, err := m.address(method.URL, map[string]string{
		definition.VariableCallbackURL: "http://" + listener.Addr().String() + path,
	})
	var page []byte
	if err == nil {
		page, err = m.callbackPage(method)
	}
	if err != nil {
		_ = listener.Close()
		return err
	}

	arrived := make(chan struct{})
	var once sync.Once
	server := &http.Server{
		ReadHeaderTimeout: headerTimeout,
		ErrorLog:          slog.NewLogLogger(m.o.Log.Handler(), slog.LevelDebug),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_, _ = w.Write(page)
			once.Do(func() { close(arrived) })
		}),
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	defer func() {
		// The page is on its way to the browser; let it arrive.
		grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(grace); err != nil {
			_ = server.Close()
		}
		<-served
	}()

	m.o.UI.Notify("Opening the login page in your browser. If it does not open, visit:\n" + loginURL)
	if err := m.o.UI.OpenBrowser(ctx, loginURL); err != nil {
		// The user can still follow the address.
		m.o.Log.Debug("cannot open the browser", "error", err)
	}

	timeout := time.Duration(method.TimeoutSeconds * float64(time.Second))
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-arrived:
		return nil
	case <-timer.C:
		return apikit.Errorf(apikit.KindUnauthenticated, "login %q timed out: the browser did not come back within %s", method.Name, timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// callbackPage returns the page the browser lands on after the login. With
// a place to submit to, the page sends the browser on there at once, with
// an empty form; a browser without scripts shows a button for that.
func (m *Manager) callbackPage(method definition.LoginMethod) ([]byte, error) {
	title := "Login"
	if m.o.AppName != "" {
		title = m.o.AppName + " login"
	}
	var page strings.Builder
	fmt.Fprintf(&page, "<!doctype html>\n<html lang=\"en\">\n<head><meta charset=\"utf-8\"><title>%s</title></head>\n<body>\n", html.EscapeString(title))
	if method.Submit == nil {
		page.WriteString("<p>Login complete. You can close this window and return to the terminal.</p>\n")
	} else {
		action, err := m.address(*method.Submit, nil)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&page, "<p>Login complete. Opening the response…</p>\n"+
			"<form id=\"next\" method=\"%s\" action=\"%s\">\n"+
			"<noscript><button type=\"submit\">Show the response</button></noscript>\n"+
			"</form>\n"+
			"<script>document.getElementById(\"next\").submit();</script>\n",
			html.EscapeString(strings.ToLower(method.Submit.Method)), html.EscapeString(action))
	}
	page.WriteString("</body>\n</html>\n")
	return []byte(page.String()), nil
}

// pasted reads the session out of what a user copied from the browser: a
// JSON object with the fields the definition names, a JSON string that
// holds a token, or else a token as it is.
func pasted(fields definition.SessionFields, text string) (session, error) {
	text = strings.TrimSpace(text)
	value, _ := apikit.Unmarshal([]byte(text))
	switch value := value.(type) {
	case *apikit.Object:
		return sessionFrom(fields, value)
	case string:
		return session{Token: strings.TrimSpace(value)}, nil
	}
	return session{Token: text}, nil
}
