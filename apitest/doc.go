// Package apitest provides a fake API for tests of code that talks to a REST
// API through api-kit: of the engine itself, and of the definitions other
// tools embed.
//
// A [Server] answers requests from a script and records them. It runs
// inside the test process and opens no socket, so tests stay fast and can
// run in parallel, and requests to the real hosts a definition names never
// leave the process:
//
//	api := apitest.New(t)
//	api.Handle("GET", "/v1/pets/0a1b2c3d", apitest.Response{
//		Body: map[string]any{"id": "0a1b2c3d", "name": "Rex"},
//	})
//
//	shop, err := engine.New(engine.Options{Definition: doc, HTTP: api.Client()})
//	// ...
//	result, err := shop.Get(ctx, engine.GetRequest{
//		Resource: "pets", ID: "0a1b2c3d",
//	})
//	// api.Requests() now holds the request the engine sent.
//
// A request the script has no answer for fails the test.
//
// [PetShop] is the definition of an invented API for tests that have no
// definition of their own.
package apitest
