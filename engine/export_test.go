package engine

import (
	"context"
	"net/http"
)

// The tests of this package live in package engine_test. What follows
// hands them the building blocks that joins, calls and lookups are built
// on, which have no exported form of their own.

// Call is a request as [Engine.Request] takes it.
type Call struct {
	Method, URL        string
	Header             http.Header
	Body               any
	Token              string
	Verb, Resource, ID string
}

// Request sends c the way a call does.
func (e *Engine) Request(ctx context.Context, c Call) (any, error) {
	return e.request(ctx, call{
		method: c.Method, url: c.URL, header: c.Header, body: c.Body, token: c.Token,
		verb: c.Verb, resource: c.Resource, id: c.ID,
	})
}

// URL returns the URL of what ref names on the API a request names itself.
func (e *Engine) URL(api string, ref Ref, query []Filter) (string, error) {
	return e.url(api, ref, query)
}

// Fetch gets what ref names.
func (e *Engine) Fetch(ctx context.Context, ref Ref, query []Filter) (any, error) {
	return e.fetch(ctx, ref, query)
}

// FetchAll gets every page of what ref names.
func (e *Engine) FetchAll(ctx context.Context, ref Ref, query []Filter, pageSize int) (any, error) {
	return e.fetchAll(ctx, ref, query, pageSize)
}

// EncodeQuery returns query as the query of a URL.
func EncodeQuery(query []Filter) string { return encodeQuery(query) }

// Upsert returns query with key set to value.
func Upsert(query []Filter, key, value string) []Filter { return upsert(query, key, value) }

// CleanBody returns the body of a failed request as it is shown.
func CleanBody(body string) string { return cleanBody(body) }
