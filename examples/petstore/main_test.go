package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/cli"
)

// run runs the tool against a fake API with the token "secret".
func run(t *testing.T, api *apitest.Server, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	o := options()
	o.HTTP = api.Client()
	o.Getenv = func(name string) string {
		if name == "PETSTORE_TOKEN" {
			return "secret"
		}
		return ""
	}
	o.Streams = cli.Streams{In: strings.NewReader(""), Out: &out, Err: &errOut}
	code = cli.New(o).Execute(context.Background(), args)
	return out.String(), errOut.String(), code
}

func TestTheDefinitionMakesATool(t *testing.T) {
	api := apitest.New(t)
	api.Handle("GET", "/v1/pets?limit=100&species=cat", apitest.Response{
		Body: json.RawMessage(`[{"id": "0a1b2c3e", "tag": "TOM-2", "name": "Tom", "species": "cat", "ownerId": "0000aaaa"}]`),
	})
	api.Handle("GET", "/v1/owners/0000aaaa", apitest.Response{
		Body: json.RawMessage(`{"id": "0000aaaa", "number": 4711, "name": "Ada", "city": "Linz"}`),
	})
	stdout, stderr, code := run(t, api, "get", "pets", "--filter", "species=cat")
	want := "TAG    NAME  SPECIES  OWNER\nTOM-2  Tom   cat      Ada\n"
	if code != apikit.ExitOK || stdout != want {
		t.Fatalf("exit code = %d, stderr = %q, stdout:\n%s\nwant:\n%s", code, stderr, stdout, want)
	}

	api.Handle("PATCH", "/v1/pets/0a1b2c3e", apitest.Response{Status: 204})
	stdout, stderr, code = run(t, api, "rename", "pets", "0a1b2c3e", "--name", "Rex")
	if code != apikit.ExitOK || stdout != "Renamed 0a1b2c3e to Rex\n" {
		t.Fatalf("rename: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	requests := api.Requests()
	last := requests[len(requests)-1]
	if got := apikit.Text(last.Body); got != `{"name": "Rex"}` || last.Header.Get("Authorization") != "Bearer secret" {
		t.Errorf("the call sent %s with the header %q", got, last.Header.Get("Authorization"))
	}
}
