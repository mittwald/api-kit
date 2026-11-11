package engine

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/mittwald/api-kit/definition"
)

// maxRedirects is how many requests the Engine's own client sends for
// one of the Engine's, redirects included: as many as the default client
// of package http.
const maxRedirects = 10

// errRedirects is what a request fails with that is redirected too often.
// Sending it again would only walk the same circle.
var errRedirects = fmt.Errorf("stopped after %d redirects", maxRedirects)

// credentialKey is the context key under which a request carries the names
// of the headers that hold its credential.
type credentialKey struct{}

// NewClient returns the client an Engine uses when it is given none, for
// callers that send requests of their own to the same API and want them
// treated alike: package auth, for one. insecure makes it accept any TLS
// certificate.
//
// The timeout applies to connecting and to the wait for the headers of the
// response, not to the request as a whole, which would cut off a long
// listing that is still arriving.
func NewClient(settings definition.HTTP, insecure bool) *http.Client {
	timeout := seconds(settings.TimeoutSeconds)
	// Joins send the most requests at once; the connections are sized for
	// them.
	connections := settings.JoinMaxWorkers
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxConnsPerHost:       connections,
		MaxIdleConns:          connections,
		MaxIdleConnsPerHost:   connections,
		IdleConnTimeout:       90 * time.Second,
	}
	if insecure {
		//nolint:gosec // The user asked for it, to reach an API whose certificate cannot be verified.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: transport, CheckRedirect: checkRedirect}
}

// checkRedirect keeps the credential of a request from following a
// redirect to another host. Package http does so for the headers it knows;
// a definition can put the credential into any header.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errRedirects
	}
	if req.URL.Host != via[0].URL.Host {
		names, _ := req.Context().Value(credentialKey{}).([]string)
		for _, name := range names {
			delete(req.Header, name)
		}
	}
	return nil
}

// withCredential adds the credential to req and returns the request with
// the names of the headers that hold it, for [checkRedirect].
func (e *Engine) withCredential(req *http.Request, token string) *http.Request {
	before := req.Header.Clone()
	e.authorize(req, token)
	var names []string
	for name, values := range req.Header {
		if !slices.Equal(values, before[name]) {
			names = append(names, name)
		}
	}
	return req.WithContext(context.WithValue(req.Context(), credentialKey{}, names))
}
