package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// session is a token with what is needed to renew it.
type session struct {
	// Token is the token requests are sent with.
	Token string
	// RefreshToken renews the session once; "" means it cannot be renewed.
	RefreshToken string
	// ExpiresAt is when Token expires; the zero time means it is unknown.
	ExpiresAt time.Time
}

// record is the form a session is stored in.
type record struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
}

// encode returns the session as the JSON object that is stored.
func (s session) encode() (string, error) {
	stored := record{Token: s.Token, RefreshToken: s.RefreshToken}
	if !s.ExpiresAt.IsZero() {
		stored.ExpiresAt = s.ExpiresAt.UTC().Format(time.RFC3339)
	}
	data, err := apikit.Marshal(stored)
	return string(data), err
}

// decodeSession reads a stored value: the JSON object a Manager writes, or
// anything else as a token and nothing more, which is how a value that was
// put there by hand looks.
func decodeSession(value string) (session, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") {
		return session{Token: value}, nil
	}
	var stored record
	if err := json.Unmarshal([]byte(value), &stored); err != nil {
		return session{}, errors.New("it is not the JSON object of a session")
	}
	if stored.Token = strings.TrimSpace(stored.Token); stored.Token == "" {
		return session{}, nil
	}
	return session{Token: stored.Token, RefreshToken: stored.RefreshToken, ExpiresAt: parseExpiry(stored.ExpiresAt)}, nil
}

// parseExpiry reads the time a token expires at: a date with a time of day,
// or a date alone. A time without a zone is UTC. Text that is no time
// yields the zero time: an expiry that cannot be read is an unknown one.
func parseExpiry(text string) time.Time {
	text = strings.Replace(strings.TrimSpace(text), " ", "T", 1)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", time.DateOnly} {
		if expiry, err := time.Parse(layout, text); err == nil {
			return expiry
		}
	}
	return time.Time{}
}

// load reads the stored session. A store that holds none, or that does not
// work, yields the zero session.
func (m *Manager) load() session {
	if m.o.Store == nil {
		return session{}
	}
	value, err := m.o.Store.Get(m.o.Account)
	if err == nil {
		var stored session
		if stored, err = decodeSession(value); err == nil {
			return stored
		}
	}
	if !errors.Is(err, ErrNotFound) {
		m.o.Log.Warn("cannot read the stored session", "account", m.o.Account, "error", err)
	}
	return session{}
}

// save stores a session and returns it as it was stored.
func (m *Manager) save(fresh session) (session, error) {
	if m.o.Store == nil {
		return fresh, nil
	}
	// A session that comes without a refresh token, for a token that is
	// stored with one, keeps it: whoever could not name the refresh token
	// must not turn a session that can be renewed into one that needs
	// another login.
	if fresh.RefreshToken == "" {
		if stored := m.load(); stored.Token == fresh.Token {
			fresh.RefreshToken = stored.RefreshToken
			if fresh.ExpiresAt.IsZero() {
				fresh.ExpiresAt = stored.ExpiresAt
			}
		}
	}
	value, err := fresh.encode()
	if err == nil {
		err = m.o.Store.Set(m.o.Account, value)
	}
	return fresh, err
}

// expiring reports whether the stored session should be renewed before it
// is used: it can be, and it expires within the leeway of the definition.
// An expiry that is unknown is not one that is near.
func (m *Manager) expiring(stored session) bool {
	refresh := m.o.Config.Refresh
	if refresh == nil || stored.RefreshToken == "" || stored.ExpiresAt.IsZero() || m.rejected[stored.Token] {
		return false
	}
	leeway := time.Duration(refresh.LeewaySeconds * float64(time.Second))
	return stored.ExpiresAt.Sub(m.o.Now()) <= leeway
}

// sessionFromJSON reads the session out of a response.
func sessionFromJSON(fields definition.SessionFields, body []byte) (session, error) {
	value, err := apikit.Unmarshal(body)
	object, ok := value.(*apikit.Object)
	if err != nil || !ok {
		return session{}, errors.New("the response is not a JSON object")
	}
	fresh, err := sessionFrom(fields, object)
	if err == nil && fresh.Token == "" {
		err = errors.New("the response holds no token")
	}
	return fresh, err
}

// sessionFrom reads the parts of a session out of a JSON object. A part
// that is absent is empty.
func sessionFrom(fields definition.SessionFields, object *apikit.Object) (session, error) {
	var parts [3]string
	for i, field := range []definition.SessionField{fields.Token, fields.RefreshToken, fields.ExpiresAt} {
		var err error
		if parts[i], err = fieldOf(field, object); err != nil {
			return session{}, err
		}
	}
	return session{Token: parts[0], RefreshToken: parts[1], ExpiresAt: parseExpiry(parts[2])}, nil
}

// fieldOf returns the first string a field of the definition leads to that
// is not empty, cut down to what the pattern of the field matches. The
// error for a string the pattern does not match names the path, never the
// string.
func fieldOf(field definition.SessionField, object *apikit.Object) (string, error) {
	for _, path := range field.Paths {
		text, _ := path.Get(object, "").(string)
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		if field.Match == nil {
			return text, nil
		}
		if match := field.Match.FindString(text); match != "" {
			return match, nil
		}
		return "", fmt.Errorf("the value of %s does not match %s", path, field.Match)
	}
	return "", nil
}
