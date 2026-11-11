// Package engine talks to the API a definition describes. It builds the
// URLs, sends the requests, and returns what the API answers as values of
// the types [apikit.Decode] produces.
//
// An [Engine] is made from a loaded definition with [New] and lives for one
// run of a tool:
//
//	shop, err := engine.New(engine.Options{Definition: doc, Tokens: tokens})
//	// ...
//	result, err := shop.Get(ctx, engine.GetRequest{
//		Resource: "pets",
//		Filters:  []engine.Filter{{Key: "species", Value: "cat"}},
//		All:      true,
//	})
//	// result.Items() are all cats, however many pages they fill.
//
// [Engine.Get] fetches an item, a listing or a sub-resource of an item.
// [Engine.Item] fetches one item, and [Engine.ResolveID] turns a value
// that names an item, such as a number printed on an invoice, into its ID.
//
// [Engine.Project] lays items out in the columns of the definition and
// adds the values of related resources that its joins name; [Engine.Link]
// builds the link to an item in a web interface.
//
// [Engine.Apply] runs a call: one or more requests that change a resource,
// with parameters, templates, conditions, and steps before and after. A
// call is checked as a whole before anything is sent.
//
// # Requests
//
// The definition decides where a request goes: the API of the resource,
// its path, and the query parameters that page through a listing. The
// query of a URL is sorted, so that the same request always has the same
// URL.
//
// An Engine remembers the answers to its GET requests by URL, and requests
// that are sent at the same time for the same URL share one answer. A GET
// is sent again after a timeout, a lost connection or one of the statuses
// the definition names; other requests are never repeated.
//
// The credential comes from a [TokenSource]. When the API rejects it with
// status 401, the engine asks the source for a replacement once and sends
// the request again.
//
// # Errors
//
// Nothing here prints or exits. Errors carry a kind of package apikit: a
// 404 for a GET and a value that names no item are of kind
// [apikit.KindNotFound], a request that cannot be made as asked is of kind
// [apikit.KindUsage]. An answer of the API that is no success wraps an
// [*HTTPError] with the status and the body.
package engine
