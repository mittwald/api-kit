package cli_test

import (
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
)

// The pets the fake API lists.
const petList = `[
  {"id": "0a1b2c3d", "tag": "REX-7", "name": "Rex", "species": "dog", "ownerId": "11111111", "age": 3},
  {"id": "0a1b2c3e", "tag": "TOM-2", "name": "Tom", "species": "cat", "ownerId": "22222222", "age": 12},
  {"id": "0a1b2c3f", "tag": "ABE-9", "name": "Abe", "species": "cat", "ownerId": "11111111", "age": 5}
]`

// listPets scripts the listing of pets with the default page size.
func listPets(h *tool) {
	h.api.Handle("GET", adminAPI+"/pets?limit=100", apitest.Response{Body: raw(petList)})
}

func TestGetListsAsATable(t *testing.T) {
	h := newTool(t)
	listPets(h)
	stdout, stderr, code := h.run("get", "pets")
	if code != apikit.ExitOK || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	want := "" +
		"TAG    NAME  SPECIES  OWNERID\n" +
		"REX-7  Rex   dog      11111111\n" +
		"TOM-2  Tom   cat      22222222\n" +
		"ABE-9  Abe   cat      11111111\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}

	requests := h.api.Requests()
	if len(requests) != 1 {
		t.Fatalf("%d requests, want 1", len(requests))
	}
	if got := requests[0].Header.Get("Authorization"); got != "Bearer secret" {
		t.Errorf("Authorization = %q, want the token of --token", got)
	}
}

func TestGetFormats(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{"id", "0a1b2c3d\n0a1b2c3e\n0a1b2c3f\n"},
		{"name", "Rex\nTom\nAbe\n"},
		{"columns=tag,age", "REX-7\t3\nTOM-2\t12\nABE-9\t5\n"},
		{"jsonpath=$.name", "Rex\nTom\nAbe\n"},
		{`jsonpath=$[?(@.species == "cat")].tag`, "TOM-2\nABE-9\n"},
		{"csv", "tag,name,species,ownerId\nREX-7,Rex,dog,11111111\nTOM-2,Tom,cat,22222222\nABE-9,Abe,cat,11111111\n"},
	}
	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			h := newTool(t)
			listPets(h)
			stdout, stderr, code := h.run("get", "pets", "-o", tt.format)
			if code != apikit.ExitOK || stderr != "" || stdout != tt.want {
				t.Errorf("exit code = %d, stderr = %q, stdout = %q, want %q", code, stderr, stdout, tt.want)
			}
		})
	}

	h := newTool(t)
	listPets(h)
	stdout, _, _ := h.run("get", "pets", "-o", "json")
	if !strings.HasPrefix(stdout, "[\n  {\n    \"id\": \"0a1b2c3d\",\n    \"tag\": \"REX-7\",\n") {
		t.Errorf("JSON does not keep the order of the API's keys:\n%s", stdout)
	}
	stdout, _, _ = h.run("get", "pets", "-o", "yaml")
	if !strings.HasPrefix(stdout, "  - id: 0a1b2c3d\n    tag: REX-7\n") {
		t.Errorf("YAML does not keep the order of the API's keys:\n%s", stdout)
	}
}

func TestGetOneItemAndSubResource(t *testing.T) {
	h := newTool(t)
	h.api.Handle("GET", mainAPI+"/pets/0a1b2c3d", apitest.Response{Body: raw(`{"id": "0a1b2c3d", "tag": "REX-7", "name": "Rex", "species": "dog", "ownerId": "11111111"}`)})
	// A resource may be named in the singular.
	stdout, stderr, code := h.run("get", "pet", "0a1b2c3d", "-o", "name")
	if code != apikit.ExitOK || stdout != "Rex\n" {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	h.api.Handle("GET", adminAPI+"/pets/0a1b2c3d/visits?limit=100", apitest.Response{Body: raw(`[{"id": "v1", "date": "2026-03-01", "reason": "checkup"}]`)})
	stdout, stderr, code = h.run("get", "pets", "0a1b2c3d", "visit")
	if code != apikit.ExitOK || stdout != "DATE        REASON\n2026-03-01  checkup\n" {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestGetFiltersAndPages(t *testing.T) {
	h := newTool(t)
	h.api.Handle("GET", adminAPI+"/pets?limit=2&skip=4&species=cat&ownerId=11111111", apitest.Response{Body: raw(`[]`)})
	_, stderr, code := h.run("get", "pets", "-f", "species=cat,ownerId=11111111", "-L", "2", "-S", "4")
	if code != apikit.ExitOK || stderr != "" {
		t.Errorf("exit code = %d, stderr = %q", code, stderr)
	}

	h.api.Handle("GET", adminAPI+"/pets?limit=100&page=3&species=dog", apitest.Response{Body: raw(`[]`)})
	if _, stderr, code = h.run("get", "pets", "--filter", "species=dog", "--page", "3"); code != apikit.ExitOK {
		t.Errorf("--page: exit code = %d, stderr = %q", code, stderr)
	}

	// With one worker the pages are fetched one after the other, until one
	// comes back short.
	h.env["SHOP_PAGINATION_MAX_WORKERS"] = "1"
	h.api.Handle("GET", adminAPI+"/pets?limit=2&skip=0", apitest.Response{Body: raw(`[{"id": "0a1b2c3d", "name": "Rex"}, {"id": "0a1b2c3e", "name": "Tom"}]`)})
	h.api.Handle("GET", adminAPI+"/pets?limit=2&skip=2", apitest.Response{Body: raw(`[{"id": "0a1b2c3f", "name": "Abe"}]`)})
	stdout, stderr, code := h.run("get", "pets", "--all", "-L", "2", "-o", "name")
	if code != apikit.ExitOK || stdout != "Rex\nTom\nAbe\n" {
		t.Errorf("--all: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestGetSorts(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-s", "name"}, "Abe\nRex\nTom\n"},
		{[]string{"-s", "NAME", "-d"}, "Tom\nRex\nAbe\n"},
		// Numbers are ordered by value, not as text.
		{[]string{"--sort-by", "age"}, "Rex\nAbe\nTom\n"},
		{[]string{"--sort-by", "age", "--sort-desc"}, "Tom\nAbe\nRex\n"},
		// Items that are equal keep the order of the API.
		{[]string{"-s", "species"}, "Tom\nAbe\nRex\n"},
	}
	for _, tt := range tests {
		h := newTool(t)
		listPets(h)
		stdout, stderr, code := h.run(append([]string{"get", "pets", "-o", "name"}, tt.args...)...)
		if code != apikit.ExitOK || stdout != tt.want {
			t.Errorf("%v: exit code = %d, stdout = %q, stderr = %q; want %q", tt.args, code, stdout, stderr, tt.want)
		}
	}

	h := newTool(t)
	listPets(h)
	stdout, stderr, code := h.run("get", "pets", "-s", "weight")
	if code != apikit.ExitUsage || stdout != "" || !strings.Contains(stderr, `cannot sort by "weight"`) {
		t.Errorf("an unknown field: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestGetEnvironmentAndAPI(t *testing.T) {
	h := newTool(t)
	h.api.Handle("GET", stagingAPI+"/pets?limit=100", apitest.Response{Body: raw(`[{"id": "0a1b2c3d", "name": "Staged"}]`)})
	if stdout, stderr, code := h.run("get", "pets", "--env", "staging", "-o", "name"); code != apikit.ExitOK || stdout != "Staged\n" {
		t.Errorf("--env staging: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	h.api.Handle("GET", mainAPI+"/pets?limit=100", apitest.Response{Body: raw(`[{"id": "0a1b2c3d", "name": "Main"}]`)})
	if stdout, stderr, code := h.run("get", "pets", "--api", "main", "-o", "name"); code != apikit.ExitOK || stdout != "Main\n" {
		t.Errorf("--api main: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	_, stderr, code := h.run("get", "pets", "--env", "test")
	if code != apikit.ExitUsage || !strings.Contains(stderr, `unknown environment "test", the definition has: production, staging`) {
		t.Errorf("an unknown environment: exit code = %d, stderr = %q", code, stderr)
	}
	_, stderr, code = h.run("get", "pets", "--api", "public")
	if code != apikit.ExitUsage || !strings.Contains(stderr, `unknown API "public"`) {
		t.Errorf("an unknown API: exit code = %d, stderr = %q", code, stderr)
	}
}

func TestGetFailures(t *testing.T) {
	h := newTool(t)

	// A mistake on the command line is reported before anything is sent.
	usage := []struct {
		args []string
		want string
	}{
		{[]string{"get"}, "accepts between 1 and 3 arg(s), received 0"},
		{[]string{"get", "pets", "a", "b", "c"}, "accepts between 1 and 3 arg(s), received 4"},
		{[]string{"get", "pets", "-o", "xml"}, `invalid output format "xml"`},
		{[]string{"get", "pets", "-o", "jsonpath"}, "output format jsonpath needs an expression"},
		{[]string{"get", "pets", "--page", "1", "--skip", "2"}, "[page skip]"},
		{[]string{"get", "pets", "--limit", "many"}, `invalid argument "many"`},
		{[]string{"get", "pets", "-f", "=dog"}, "filter"},
		{[]string{"get", "orders"}, "orders requires filter(s): ownerId"},
	}
	for _, tt := range usage {
		stdout, stderr, code := h.run(tt.args...)
		if code != apikit.ExitUsage || stdout != "" || !strings.Contains(stderr, tt.want) || !strings.Contains(stderr, "Run 'shop get --help' for usage.") {
			t.Errorf("%v: exit code = %d, stdout = %q, stderr = %q; want a usage error with %q", tt.args, code, stdout, stderr, tt.want)
		}
	}
	if sent := h.api.Requests(); len(sent) != 0 {
		t.Errorf("a mistake on the command line sent %d requests", len(sent))
	}

	h.api.Handle("GET", mainAPI+"/pets/0a1b2c3d", apitest.Response{Status: 404, Body: raw(`{"message": "no such pet"}`)})
	stdout, stderr, code := h.run("get", "pets", "0a1b2c3d")
	if code != apikit.ExitNotFound || stdout != "" || !strings.HasPrefix(stderr, "ERROR: ") || !strings.Contains(stderr, "0a1b2c3d") || strings.Contains(stderr, "--help") {
		t.Errorf("an item that does not exist: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	h.api.Handle("GET", adminAPI+"/owners?limit=100", apitest.Response{Status: 500, Raw: "<html><body><h1>Broken</h1></body></html>"})
	h.env["SHOP_HTTP_RETRIES"] = "0"
	stdout, stderr, code = h.run("get", "owners")
	if code != apikit.ExitFailed || stdout != "" || !strings.Contains(stderr, "500") || !strings.Contains(stderr, "Broken") || strings.Contains(stderr, "<h1>") {
		t.Errorf("a server error: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	_, stderr, code = h.run("get", "receipts")
	if code != apikit.ExitFailed || !strings.Contains(stderr, "receipts cannot be listed directly") {
		t.Errorf("a resource that cannot be listed: exit code = %d, stderr = %q", code, stderr)
	}
}

func TestGetWithoutAToken(t *testing.T) {
	h := newTool(t)
	stdout, stderr, code := h.runBare("get", "pets")
	if code != apikit.ExitFailed || stdout != "" || !strings.HasPrefix(stderr, "ERROR: ") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if sent := h.api.Requests(); len(sent) != 0 {
		t.Errorf("a run without a token sent %d requests", len(sent))
	}
}

func TestFailEmpty(t *testing.T) {
	h := newTool(t)
	h.api.Handle("GET", adminAPI+"/pets?limit=100&species=fish", apitest.Response{Body: raw(`[]`)})
	listPets(h)

	stdout, stderr, code := h.run("get", "pets", "-f", "species=fish")
	if code != apikit.ExitOK || stdout != "" || stderr != "" {
		t.Errorf("an empty result: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	stdout, stderr, code = h.run("get", "pets", "-f", "species=fish", "--fail-empty")
	if code != apikit.ExitSkipped || stdout != "" || stderr != "" {
		t.Errorf("an empty result with --fail-empty: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, _, code = h.run("get", "pets", "--fail-empty", "-o", "id"); code != apikit.ExitOK {
		t.Errorf("a result with --fail-empty: exit code = %d", code)
	}
	// Nothing selected is nothing printed, whatever the API returned.
	if _, _, code = h.run("get", "pets", "--fail-empty", "-o", "jsonpath=$.missing"); code != apikit.ExitSkipped {
		t.Errorf("an expression that selects nothing, with --fail-empty: exit code = %d", code)
	}
	// A failure keeps its own exit code.
	if _, _, code = h.run("get", "pets", "--fail-empty", "-o", "xml"); code != apikit.ExitUsage {
		t.Errorf("a mistake with --fail-empty: exit code = %d", code)
	}
}

func TestVerboseLogsRequests(t *testing.T) {
	h := newTool(t)
	listPets(h)
	stdout, stderr, code := h.run("get", "pets", "--verbose", "-o", "id")
	if code != apikit.ExitOK || stdout != "0a1b2c3d\n0a1b2c3e\n0a1b2c3f\n" {
		t.Fatalf("exit code = %d, stdout = %q", code, stdout)
	}
	for _, want := range []string{"Version: 1.2.3\n", "GET", adminAPI + "/pets?limit=100", "status=200"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "secret") {
		t.Errorf("the log shows the token:\n%s", stderr)
	}
}

func TestTunablesFromTheEnvironment(t *testing.T) {
	h := newTool(t)
	h.env["SHOP_HTTP_TIMEOUT"] = "soon"
	h.env["SHOP_HTTP_RETRIES"] = "-1"
	h.env["SHOP_JOIN_MAX_WORKERS"] = "2.5"
	h.env["SHOP_PAGINATION_MAX_WORKERS"] = " 3 "
	_, stderr, code := h.runBare("config")
	if code != apikit.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{
		`WARNING: Ignoring environment variable SHOP_HTTP_TIMEOUT="soon": expected a number`,
		`WARNING: Ignoring environment variable SHOP_HTTP_RETRIES="-1": must be at least 0`,
		`WARNING: Ignoring environment variable SHOP_JOIN_MAX_WORKERS="2.5": expected a whole number`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "PAGINATION") {
		t.Errorf("a valid value caused a warning:\n%s", stderr)
	}
}
