// Package auth supplies the token that the requests of a tool carry. It
// works from the auth section of a definition, [definition.Auth], and knows
// no API of its own: the header, the sources of tokens and the requests
// that check, renew and obtain them all come from there.
//
// A [Manager] is made once per run:
//
//	manager, err := auth.New(auth.Options{
//		Config:  doc.Auth,
//		BaseURL: func(api string) (string, error) { ... },
//		Store:   store,
//		Account: "production",
//		UI:      ui,
//	})
//
// # Sources
//
// A token given for the run itself, [Options.Explicit], is the only one
// that is tried: it is never renewed and nobody is asked for another one.
// Without it the candidates are, in this order, the token an earlier call
// settled on and the sources the definition lists: the session in the
// [Store] and environment variables. An interactive run falls back to a
// login when all of them fail.
//
// # Requests
//
// [Manager.Token] answers without asking the API: the first candidate is
// used, and the request it is sent with shows whether it is good. The
// caller reports the outcome, which is what package engine does:
//
//	token, err := manager.Token(ctx)
//	manager.Authorize(request, token)
//	// ... send the request ...
//	if response.StatusCode == http.StatusUnauthorized {
//		token, err = manager.Rejected(ctx, token) // send once more with it
//	} else {
//		manager.Confirmed(token)
//	}
//
// [Manager.Resolve] and [Manager.Status] ask the API about each candidate
// instead. The answer is one of three: the token is valid, it is invalid,
// or the API gave no verdict, with a server error for example. A token
// without a verdict is not used, it is not given up either, and it never
// leads to a login; the run fails with [ErrInconclusive]. A definition
// without a validation request takes every token as it is.
//
// # Sessions
//
// A session is a token with a refresh token and an expiry. A stored session
// that is about to expire is renewed before it is used. A session whose
// token the API rejects is renewed before the token is given up, whether it
// comes from the store or from an environment variable that the definition
// pairs with one for the refresh token. A renewed session always goes to
// the store.
//
// A refresh token is good for one use. A Manager therefore renews one
// session at a time, and callers that are rejected together share the
// outcome. Before it spends a refresh token it reads the store anew, and
// leaves a session alone that another process has renewed in the meantime.
//
// A stored token is never removed on its own account. When the API rejects
// it and its session cannot be renewed, calls fail with a [*StaleError]
// until a login replaces it or [Manager.Logout] removes it.
//
// # The store
//
// The store holds one value per account, a JSON object:
//
//	{"token": "...", "refreshToken": "...", "expiresAt": "2026-03-09T15:00:00Z"}
//
// The refresh token and the expiry may be absent. A value that is no JSON
// object is read as a token and nothing more. An expiry without a time zone
// is in UTC.
//
// A Store tells a Manager that it holds nothing for an account with
// [ErrNotFound]. A Store of another library has an error of its own for
// that and needs a wrapper:
//
//	func (s store) Get(account string) (string, error) {
//		value, err := s.inner.Get(account)
//		if errors.Is(err, other.ErrNotFound) {
//			return "", auth.ErrNotFound
//		}
//		return value, err
//	}
//
// # Logins
//
// [Manager.Login] runs one of the login methods of the definition. A
// password login asks its questions through the [UI] and sends the answers
// to the API, which responds with the session. A browser login opens a page
// of the API and waits on the loopback interface for the browser to come
// back; the user then copies what the browser shows into a prompt. The new
// token is put to the API before it is stored.
//
// Nothing in this package prints, and no token is ever logged or put into
// an error.
package auth
