package auth_test

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/definition"
)

func ExampleManager() {
	doc, _, err := definition.Load(definition.Source{Name: "petshop.yaml", Data: []byte(`
environments:
  production:
    apis:
      main: https://api.example.test/v1
auth:
  header: {name: X-Token, value: "${token}"}
  sources:
    - store
    - {env: PETSHOP_TOKEN}
`)}, definition.LoadOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}
	environment := map[string]string{"PETSHOP_TOKEN": "t-17"}

	manager, err := auth.New(auth.Options{
		Config: doc.Auth,
		BaseURL: func(api string) (string, error) {
			url, _ := doc.API("production", api)
			return url, nil
		},
		Getenv: func(name string) string { return environment[name] },
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	ctx := context.Background()
	source, validated, err := manager.Status(ctx)
	fmt.Println(source, validated, err)

	token, err := manager.Token(ctx)
	if err != nil {
		fmt.Println(err)
		return
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.test/v1/pets", nil)
	manager.Authorize(request, token)
	fmt.Println(request.Header.Get("X-Token"))
	// Output:
	// environment variable PETSHOP_TOKEN false <nil>
	// t-17
}
