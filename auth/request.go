package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// maxResponse is the most that is read of the response to a request of
// this package. A session is far smaller.
const maxResponse = 1 << 20

// address returns the URL of a request of the definition: the base URL of
// its API, its path and its query parameters, with the variables filled in.
func (m *Manager) address(request definition.AuthRequest, variables map[string]string) (string, error) {
	base, err := m.o.BaseURL(request.API)
	if err != nil {
		return "", err
	}
	// A value becomes one segment of the path, whatever it holds.
	path, _ := expr.ParseTemplate(request.Path).Render(lookup(variables), expr.RenderOptions{
		Format: func(value any) string { return url.PathEscape(apikit.Text(value)) },
	})
	address := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
	separator := "?"
	if strings.Contains(address, "?") {
		separator = "&"
	}
	for _, parameter := range request.Query {
		value, _ := expr.ParseTemplate(parameter.Template).Render(lookup(variables), expr.RenderOptions{})
		address += separator + url.QueryEscape(parameter.Name) + "=" + url.QueryEscape(value)
		separator = "&"
	}
	return address, nil
}

// render returns a body of the definition with the variables filled in,
// in its keys and its strings.
func render(body any, variables map[string]string) any {
	switch value := body.(type) {
	case string:
		text, _ := expr.ParseTemplate(value).Render(lookup(variables), expr.RenderOptions{})
		return text
	case []any:
		list := make([]any, len(value))
		for i, item := range value {
			list[i] = render(item, variables)
		}
		return list
	case *apikit.Object:
		object := apikit.NewObject()
		for key, item := range value.All() {
			name, _ := render(key, variables).(string)
			object.Set(name, render(item, variables))
		}
		return object
	}
	return body
}

// send sends a request of the definition and returns the status and the
// body of the response. A token that is not empty goes into the header of
// the definition. The error never holds the URL, which may hold a token.
func (m *Manager) send(ctx context.Context, request definition.AuthRequest, variables map[string]string, token string) (int, []byte, error) {
	fail := func(err error) (int, []byte, error) {
		if inner, ok := errors.AsType[*url.Error](err); ok {
			err = inner.Err
		}
		return 0, nil, fmt.Errorf("%s %s: %w", request.Method, request.Path, err)
	}
	address, err := m.address(request, variables)
	if err != nil {
		return fail(err)
	}
	var body io.Reader
	if request.Body != nil {
		data, err := apikit.Marshal(render(request.Body, variables))
		if err != nil {
			return fail(err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, request.Method, address, body)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		m.Authorize(req, token)
	}
	response, err := m.o.HTTP.Do(req)
	if err != nil {
		return fail(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return fail(err)
	}
	m.o.Log.Debug("sent a request", "method", request.Method, "path", request.Path, "status", response.StatusCode)
	return response.StatusCode, data, nil
}

// validate asks the API about a token. An answer that proves nothing, or
// none at all, says nothing about the token: it must cost neither the
// credential nor the run.
func (m *Manager) validate(ctx context.Context, token string) verdict {
	validation := m.o.Config.Validate
	if validation == nil {
		return verdictNone
	}
	status, _, err := m.send(ctx, validation.Request, map[string]string{definition.VariableToken: token}, token)
	switch {
	case err != nil:
		m.o.Log.Warn("cannot validate the token", "error", err)
		return verdictUnknown
	case slices.Contains(validation.Valid, status):
		return verdictValid
	case slices.Contains(validation.Invalid, status):
		return verdictInvalid
	}
	m.o.Log.Debug("the validation proves nothing about the token", "status", status)
	return verdictUnknown
}

// rotate renews the session a rejected token belongs to and returns the
// new token, or "" when the session cannot be renewed. The refresh token is
// the one stored with the token, or else the one in the environment next to
// the variable that holds the token: what says where a token comes from is
// its value. A token is rotated once in a process, and the token given for
// a run never.
func (m *Manager) rotate(ctx context.Context, token string) string {
	if m.o.Config.Refresh == nil || m.o.Explicit != "" || m.rejected[token] {
		return ""
	}
	// The store is read anew right before a refresh token is spent. When it
	// holds another token by now, one this process has not seen rejected,
	// another process has renewed the session or logged in: its refresh
	// token is the one that counts, and that session is a candidate of its
	// own.
	stored := m.load()
	var refreshToken string
	switch {
	case stored.Token == token:
		refreshToken = stored.RefreshToken
	case stored.Token != "" && !m.rejected[stored.Token]:
		return ""
	default:
		refreshToken = m.refreshTokenInEnv(token)
	}
	if refreshToken == "" {
		return ""
	}
	// A rotation that hands the rejected token back has not helped.
	if fresh := m.refresh(ctx, token, refreshToken); fresh != token {
		return fresh
	}
	return ""
}

// refreshTokenInEnv returns the refresh token that the environment holds
// for a token: the value of the variable the definition pairs with the
// first variable that holds the token.
func (m *Manager) refreshTokenInEnv(token string) string {
	for _, source := range m.o.Config.Sources {
		if source.Env != "" && strings.TrimSpace(m.o.Getenv(source.Env)) == token {
			if source.RefreshEnv == "" {
				return ""
			}
			return strings.TrimSpace(m.o.Getenv(source.RefreshEnv))
		}
	}
	return ""
}

// refresh redeems a refresh token and stores the session that comes back,
// wherever the old one came from: the environment is a seed that cannot be
// written, so a session that started there goes on in the store. It
// returns the new token, or "" when the session was not renewed.
func (m *Manager) refresh(ctx context.Context, token, refreshToken string) string {
	config := m.o.Config.Refresh
	status, body, err := m.send(ctx, config.Request, map[string]string{
		definition.VariableToken:        token,
		definition.VariableRefreshToken: refreshToken,
	}, token)
	switch {
	case err != nil:
		m.o.Log.Warn("cannot refresh the session", "error", err)
		return ""
	case slices.Contains(config.Rejected, status):
		m.o.Log.Debug("the API rejected the refresh token", "status", status)
		return ""
	case status < 200 || status > 299:
		m.o.Log.Warn("cannot refresh the session", "status", status)
		return ""
	}
	fresh, err := sessionFromJSON(config.Session, body)
	if err != nil {
		m.o.Log.Warn("cannot refresh the session", "error", err)
		return ""
	}
	// The old refresh token is spent either way, so the new session is
	// kept even when it came without one.
	if _, err := m.save(fresh); err != nil {
		m.o.Log.Warn("cannot store the refreshed session", "account", m.o.Account, "error", err)
	}
	m.o.Log.Debug("refreshed the session")
	return fresh.Token
}
