package engine_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

func ExampleEngine_Get() {
	doc, _, err := definition.Load(definition.Source{Name: "petshop.yaml", Data: []byte(apitest.PetShop)}, definition.LoadOptions{})
	if err != nil {
		panic(err)
	}
	// A stand-in for the API that answers every request with two cats.
	api := doerFunc(func(req *http.Request) (*http.Response, error) {
		fmt.Println(req.Method, req.URL)
		const cats = `[{"id":"0a1b2c3d","name":"Tom"},{"id":"0b0b0b0b","name":"Kitty"}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(cats))}, nil
	})

	shop, err := engine.New(engine.Options{Definition: doc, HTTP: api})
	if err != nil {
		panic(err)
	}
	filters, err := engine.ParseFilters([]string{"species=cat"})
	if err != nil {
		panic(err)
	}
	result, err := shop.Get(context.Background(), engine.GetRequest{Resource: "pets", Filters: filters})
	if err != nil {
		panic(err)
	}
	for _, item := range result.Items() {
		cat, _ := item.(*apikit.Object)
		id, _ := shop.IDOf(result.Resource, cat)
		name, _ := cat.Get("name")
		fmt.Println(id, name)
	}
	// Output:
	// GET https://admin.example.test/v1/pets?limit=100&species=cat
	// 0a1b2c3d Tom
	// 0b0b0b0b Kitty
}

func ExampleParseFilters() {
	filters, err := engine.ParseFilters([]string{"species=cat,ownerId=4711", "born>=2026-01-31"})
	fmt.Println(filters, err)

	_, err = engine.ParseFilters([]string{"=cat"})
	fmt.Println(err)
	// Output:
	// [{species cat} {ownerId 4711} {born> 2026-01-31}] <nil>
	// filter "=cat" has no key, expected key=value
}
