package cli_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
)

// grooming gives the pet shop calls: one that changes a pet, with
// parameters of every type, and one that needs no ID.
const grooming = `
resources:
  pets:
    conditions: ["species=cat", "vaccinated=false"]
    setters:
      - name: rename
        aliases: [name]
        help: Rename a pet
        method: PATCH
        pathTemplate: pets/${id}
        payloadTemplate: {name: "${name}", note: "${note}", tags: "${tag}", weight: "${weight}", age: "${age}", fed: "${fed}", size: "${size}", owner: "${owner}", vet: "${vet}"}
        message: Renamed ${id} to ${name}
        parameters:
          - {name: name, aliases: [new-name], help: New name of the pet, template: true}
          - {name: note, required: false}
          - {name: tag, type: list, required: false}
          - {name: weight, type: float, required: false}
          - {name: age, type: int, required: false}
          - {name: fed, type: bool, required: false}
          - {name: size, required: false, choices: [small, large]}
          - {name: owner, required: false, resource: owners}
          - {name: vet, required: false, lookup: {get: owners, filters: ["city=Linz"], match: [name, number], select: id, complete: name}}
      - name: adopt
        help: Take in a new pet
        requiresId: false
        method: POST
        pathTemplate: pets
        payloadTemplate: {name: "${name}", species: "${species}"}
        parameters:
          - {name: name}
          - {name: species, required: false}
        exclusiveParameters: []
  owners:
    conditions: ["city=Linz"]
    setters:
      - {name: adopt, help: Register an owner, requiresId: false, pathTemplate: owners, payloadTemplate: {name: "${name}"}, parameters: [{name: name}]}
`

// groomer returns a tool for the pet shop with calls.
func groomer(t *testing.T) *tool {
	t.Helper()
	h := newTool(t)
	h.overlay(grooming)
	return h
}

// lastBody returns the body of the last request the tool sent, as text.
func lastBody(t *testing.T, h *tool) string {
	t.Helper()
	requests := h.api.Requests()
	if len(requests) == 0 {
		t.Fatal("the tool sent no request")
	}
	return apikit.Text(requests[len(requests)-1].Body)
}

func TestCallSendsItsParameters(t *testing.T) {
	h := groomer(t)
	h.api.Handle("PATCH", mainAPI+"/pets/0a1b2c3d", apitest.Response{Status: 204})

	stdout, stderr, code := h.run("rename", "pets", "0a1b2c3d", "--name", "Rex", "--tag", "calm", "--tag", "old", "--weight", "2.5", "--age", "3", "--fed")
	if code != apikit.ExitOK || stdout != "Renamed 0a1b2c3d to Rex\n" || stderr != "" {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	// Every value arrives with its type; what was not given is left out.
	if got, want := lastBody(t, h), `{"name": "Rex", "tags": ["calm", "old"], "weight": 2.5, "age": 3, "fed": true}`; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}

	// Several values may follow a list option at once, and a resource may
	// be named in the singular.
	if _, stderr, code = h.run("rename", "pet", "0a1b2c3d", "--tag", "calm", "old", "--name", "Rex"); code != apikit.ExitOK {
		t.Fatalf("several words for a list: exit code = %d, stderr = %q", code, stderr)
	}
	if got, want := lastBody(t, h), `{"name": "Rex", "tags": ["calm", "old"]}`; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}

	// A flag that is switched off is not given.
	if _, _, code = h.run("rename", "pets", "0a1b2c3d", "--name", "Rex", "--fed=false"); code != apikit.ExitOK {
		t.Fatalf("--fed=false: exit code = %d", code)
	}
	if got, want := lastBody(t, h), `{"name": "Rex"}`; got != want {
		t.Errorf("body = %s\nwant   %s", got, want)
	}
}

func TestCallWithoutIDShowsTheAnswer(t *testing.T) {
	h := groomer(t)
	h.api.Handle("POST", adminAPI+"/pets", apitest.Response{Status: 201, Body: raw(`{"id": "0a1b2c3e", "tag": "TOM-2", "name": "Tom", "species": "cat", "ownerId": ""}`)})

	stdout, stderr, code := h.run("adopt", "pets", "--name", "Tom", "--species", "cat")
	if code != apikit.ExitOK || stdout != "TAG    NAME  SPECIES  OWNERID\nTOM-2  Tom   cat\n" {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout, _, _ = h.run("adopt", "pets", "--name", "Tom", "-o", "id"); stdout != "0a1b2c3e\n" {
		t.Errorf("-o id: stdout = %q", stdout)
	}
}

func TestCallMistakes(t *testing.T) {
	h := groomer(t)
	tests := []struct {
		args []string
		want string
		hint string
	}{
		{[]string{"rename"}, "rename needs a resource: pets", "shop rename"},
		{[]string{"rename", "owners", "0a1b2c3d"}, "rename is not available for owners, only for pets", "shop rename"},
		{[]string{"adopt"}, "adopt needs a resource: owners, pets", "shop adopt"},
		{[]string{"rename", "pets"}, "--name", "shop rename pets"},
		{[]string{"rename", "pets", "--name", "x"}, "ID", "shop rename pets"},
		{[]string{"rename", "pets", "0a1b2c3d"}, "--name", "shop rename pets"},
		{[]string{"rename", "pets", "0a1b2c3d", "extra", "--name", "x"}, "accepts at most 1 arg(s)", "shop rename pets"},
		{[]string{"adopt", "pets", "0a1b2c3d", "--name", "x"}, "0a1b2c3d", "shop adopt pets"},
		{[]string{"rename", "pets", "0a1b2c3d", "--name", "x", "--size", "huge"}, "huge", "shop rename pets"},
		{[]string{"rename", "pets", "0a1b2c3d", "--name", "x", "--weight", "heavy"}, `"heavy"`, "shop rename pets"},
		{[]string{"rename", "pets", "0a1b2c3d", "--name", "x", "--name-template", "y"}, "[name name-template name-template-file]", "shop rename pets"},
		{[]string{"rename", "pets", "0a1b2c3d", "--name", "x", "--template-var", "novalue"}, "expected NAME=VALUE", "shop rename pets"},
		{[]string{"rename", "pets", "0a1b2c3d", "--name", "x", "--new-name", "y"}, "--new-name", "shop rename pets"},
		{[]string{"rename", "pets", "0a1b2c3d", "--name", "x", "-o", "xml"}, `invalid output format "xml"`, "shop rename pets"},
	}
	for _, tt := range tests {
		stdout, stderr, code := h.run(tt.args...)
		if code != apikit.ExitUsage || stdout != "" || !strings.Contains(stderr, tt.want) || !strings.Contains(stderr, "Run '"+tt.hint+" --help' for usage.") {
			t.Errorf("%v: exit code = %d, stdout = %q, stderr = %q; want a usage error with %q", tt.args, code, stdout, stderr, tt.want)
		}
	}
	if sent := h.api.Requests(); len(sent) != 0 {
		t.Errorf("mistakes on the command line sent %d requests", len(sent))
	}
}

func TestCallFailure(t *testing.T) {
	h := groomer(t)
	h.api.Handle("PATCH", mainAPI+"/pets/0a1b2c3d", apitest.Response{Status: 409, Body: raw(`{"message": "name is taken"}`)})
	stdout, stderr, code := h.run("rename", "pets", "0a1b2c3d", "--name", "Rex")
	if code != apikit.ExitFailed || stdout != "" || !strings.HasPrefix(stderr, "ERROR: ") || !strings.Contains(stderr, "name is taken") || strings.Contains(stderr, "--help") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestCallDryRunAndTemplates(t *testing.T) {
	h := groomer(t)
	stdout, stderr, code := h.run("rename", "pets", "0a1b2c3d", "--name-template", "Pet ${id} of ${who}", "--template-var", "who=Ada", "--tag", "calm", "--weight", "2.5", "--fed", "--dry-run")
	if code != apikit.ExitOK || stderr != "Dry run, rename was not sent\n" {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	want := "id: 0a1b2c3d\nname: Pet 0a1b2c3d of Ada\ntag:\n  - calm\nweight: 2.5\nfed: true\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	if sent := h.api.Requests(); len(sent) != 0 {
		t.Errorf("a dry run sent %d requests", len(sent))
	}

	// A text of several lines is shown as it will be sent.
	stdout, _, _ = h.run("rename", "pets", "0a1b2c3d", "--name-template", "Line one\nLine ${id}", "--dry-run")
	if !strings.Contains(stdout, "name: |-\n  Line one\n  Line 0a1b2c3d\n") {
		t.Errorf("a text of several lines:\n%s", stdout)
	}

	// The call itself sends the rendered text.
	h.api.Handle("PATCH", mainAPI+"/pets/0a1b2c3d", apitest.Response{Status: 204})
	if _, stderr, code = h.run("rename", "pets", "0a1b2c3d", "--name-template", "Pet ${id}"); code != apikit.ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if got := lastBody(t, h); got != `{"name": "Pet 0a1b2c3d"}` {
		t.Errorf("body = %s", got)
	}

	// A template variable nothing uses is a mistake.
	_, stderr, code = h.run("rename", "pets", "0a1b2c3d", "--name", "Rex", "--template-var", "who=Ada")
	if code != apikit.ExitUsage || !strings.Contains(stderr, "who") {
		t.Errorf("an unused variable: exit code = %d, stderr = %q", code, stderr)
	}
}

func TestCallConditions(t *testing.T) {
	h := groomer(t)
	h.api.Handle("GET", mainAPI+"/pets/0a1b2c3d", apitest.Response{Body: raw(`{"id": "0a1b2c3d", "species": "dog"}`)})
	h.api.Handle("PATCH", mainAPI+"/pets/0a1b2c3d", apitest.Response{Status: 204})

	// A condition that does not hold skips the call; that is no error.
	stdout, stderr, code := h.run("rename", "pets", "0a1b2c3d", "--name", "Rex", "--if", "species=cat")
	if code != apikit.ExitSkipped || stdout != "" || !strings.HasPrefix(stderr, "Skipped rename") || strings.Contains(stderr, "ERROR") || strings.Contains(stderr, "--help") {
		t.Errorf("a condition that fails: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout, _, code = h.run("rename", "pets", "0a1b2c3d", "--name", "Rex", "--if", "species=dog"); code != apikit.ExitOK || stdout != "Renamed 0a1b2c3d to Rex\n" {
		t.Errorf("a condition that holds: exit code = %d, stdout = %q", code, stdout)
	}

	// A listing that must be empty, given as several words or as one. A
	// value that starts with @ is one of the call: here its ID.
	h.api.Handle("GET", adminAPI+"/pets?limit=100&skip=0&species=cat&ownerId=0a1b2c3d", apitest.Response{Body: raw(`[{"id": "0a1b2c3e", "species": "cat"}]`)})
	for _, args := range [][]string{
		{"rename", "pets", "0a1b2c3d", "--if-none", "pets", "species=cat", "ownerId=@id", "--name", "Rex"},
		{"rename", "pets", "0a1b2c3d", "--name", "Rex", "--if-none=pets species=cat ownerId=@id"},
	} {
		if _, stderr, code = h.run(args...); code != apikit.ExitSkipped || !strings.HasPrefix(stderr, "Skipped rename") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, code, stderr)
		}
	}
	// A listing that must have an item.
	if stdout, stderr, code = h.run("rename", "pets", "0a1b2c3d", "--name", "Rex", "--if-any", "pets", "species=cat", "ownerId=@id"); code != apikit.ExitOK || stdout != "Renamed 0a1b2c3d to Rex\n" {
		t.Errorf("--if-any: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestApplyStillRunsCalls(t *testing.T) {
	h := groomer(t)
	h.api.Handle("PATCH", mainAPI+"/pets/0a1b2c3d", apitest.Response{Status: 204})

	tests := [][]string{
		// The ID before the call, as it used to be written, or after it.
		{"apply", "pets", "0a1b2c3d", "rename", "--name", "Rex"},
		{"apply", "pets", "rename", "0a1b2c3d", "--name", "Rex"},
		{"apply", "pet", "0a1b2c3d", "rename", "--name", "Rex"},
		// The aliases of calls and of parameters exist here only.
		{"apply", "pets", "0a1b2c3d", "name", "--name", "Rex"},
		{"apply", "pets", "0a1b2c3d", "rename", "--new-name", "Rex"},
	}
	for _, args := range tests {
		stdout, stderr, code := h.run(args...)
		if code != apikit.ExitOK || stdout != "Renamed 0a1b2c3d to Rex\n" {
			t.Errorf("%v: exit code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
		if stderr != "WARNING: apply is deprecated, use rename pets ID\n" {
			t.Errorf("%v: stderr = %q, want the hint at the call command", args, stderr)
		}
		if got := lastBody(t, h); got != `{"name": "Rex"}` {
			t.Errorf("%v: body = %s", args, got)
		}
	}

	for args, want := range map[string]string{
		"apply":                    "apply needs a resource and a call",
		"apply pets":               "apply pets needs a call: rename, name, adopt",
		"apply pets 0a1b2c3d":      "apply pets needs a call",
		"apply pets 0a1b2c3d nope": "nope is not available for pets",
		"apply visits rename":      "visits has no calls",
	} {
		_, stderr, code := h.run(strings.Fields(args)...)
		if code != apikit.ExitUsage || !strings.Contains(stderr, want) {
			t.Errorf("%q: exit code = %d, stderr = %q; want %q", args, code, stderr, want)
		}
	}

	// The older command is not advertised, the call commands are.
	stdout, _, _ := h.runBare("--help")
	if strings.Contains(stdout, "  apply") || !strings.Contains(stdout, "  rename      Rename a pet") || !strings.Contains(stdout, "  adopt ") {
		t.Errorf("help:\n%s", stdout)
	}
}

func TestCallHelp(t *testing.T) {
	h := groomer(t)
	stdout, _, code := h.runBare("rename", "pets", "--help")
	if code != apikit.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{
		"Rename a pet", "shop rename pets ID [flags]", "pets, pet",
		"--name string", "New name of the pet (required)", "--name-template string", "--name-template-file string",
		"--tag stringArray", "--weight float", "--age int", "--fed", "--if stringArray", "--if-none", "--if-any",
		"--template string", "--template-var stringArray", "--dry-run",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the help of the call lacks %q:\n%s", want, stdout)
		}
	}
	// Aliases of parameters belong to apply.
	if strings.Contains(stdout, "new-name") {
		t.Errorf("the help of the call command shows a parameter alias:\n%s", stdout)
	}
	// A call without template parameters has no template options.
	if stdout, _, _ = h.runBare("adopt", "pets", "--help"); strings.Contains(stdout, "--dry-run") || strings.Contains(stdout, "--template") {
		t.Errorf("a call without template parameters has template options:\n%s", stdout)
	}
}

func TestCompleteCalls(t *testing.T) {
	h := groomer(t)
	if offers, _ := complete(t, h, "ren"); !slices.Equal(offers, []string{"rename"}) {
		t.Errorf("commands starting with ren = %v", offers)
	}
	if offers, _ := complete(t, h, "adopt", ""); !slices.Equal(offers, []string{"owners", "pets"}) {
		t.Errorf("resources of adopt = %v", offers)
	}
	if offers, _ := complete(t, h, "rename", "pets", "0a1b2c3d", "--size", ""); !slices.Equal(offers, []string{"small", "large"}) {
		t.Errorf("choices of --size = %v", offers)
	}
	if offers, _ := complete(t, h, "rename", "pets", "0a1b2c3d", "--size", "l"); !slices.Equal(offers, []string{"large"}) {
		t.Errorf("choices of --size starting with l = %v", offers)
	}
	// The older command is not offered, but completes when typed.
	if offers, _ := complete(t, h, "app"); len(offers) != 0 {
		t.Errorf("commands starting with app = %v", offers)
	}
	if offers, _ := complete(t, h, "apply", "pets", "0a1b2c3d", "ren"); !slices.Equal(offers, []string{"rename"}) {
		t.Errorf("calls of apply pets = %v", offers)
	}
}

func TestCompleteConditions(t *testing.T) {
	h := groomer(t)
	// The conditions of the pet, and those of what a parameter names.
	offers, _ := complete(t, h, "rename", "pets", "0a1b2c3d", "--if", "")
	if !slices.Equal(offers, []string{"species=cat", "vaccinated=false", "owner.city=Linz"}) {
		t.Errorf("conditions = %v", offers)
	}
	// A call without an ID has no item of its own to test.
	if offers, _ = complete(t, h, "adopt", "pets", "--if", ""); len(offers) != 0 {
		t.Errorf("conditions of a call without an ID = %v", offers)
	}
}

func TestCompleteListChecks(t *testing.T) {
	h := groomer(t)
	// First a resource that can be listed.
	offers, _ := complete(t, h, "rename", "pets", "0a1b2c3d", "--if-none", "o")
	if !slices.Equal(offers, []string{"owners", "orders"}) {
		t.Errorf("resources = %v", offers)
	}
	// Then its filters, also with what the call can put in, and its
	// conditions.
	offers, _ = complete(t, h, "rename", "pets", "0a1b2c3d", "--if-any", "pets", "")
	want := []string{"species=", "species=@id", "species=@owner", "ownerId=", "ownerId=@id", "ownerId=@owner", "search=", "search=@id", "search=@owner", "species=cat", "vaccinated=false"}
	if !slices.Equal(offers, want) {
		t.Errorf("words after the resource = %v\nwant %v", offers, want)
	}
	if offers, _ = complete(t, h, "rename", "pets", "0a1b2c3d", "--if-none", "pet", "species=cat", "ownerId=@"); !slices.Equal(offers, []string{"ownerId=@id", "ownerId=@owner"}) {
		t.Errorf("references = %v", offers)
	}
	// Without a list check under way, the word after the call is the ID.
	if offers, _ = complete(t, h, "rename", "pets", ""); len(offers) != 0 {
		t.Errorf("offers for the ID = %v", offers)
	}
}

func TestCompleteTemplates(t *testing.T) {
	h := groomer(t)
	h.options.Templates = fstest.MapFS{
		"welcome.yaml":  {Data: []byte("parameters: {name: Rex}\n")},
		"goodbye.yml":   {Data: []byte("parameters: {name: Tom}\n")},
		"notes.txt":     {Data: []byte("no template")},
		"more/sub.yaml": {Data: []byte("parameters: {}\n")},
	}
	if offers, _ := complete(t, h, "rename", "pets", "0a1b2c3d", "--template", ""); !slices.Equal(offers, []string{"goodbye", "welcome"}) {
		t.Errorf("templates = %v", offers)
	}
	if offers, _ := complete(t, h, "rename", "pets", "0a1b2c3d", "--template", "w"); !slices.Equal(offers, []string{"welcome"}) {
		t.Errorf("templates starting with w = %v", offers)
	}
	// A built-in template gives the parameters of the call.
	h.api.Handle("PATCH", mainAPI+"/pets/0a1b2c3d", apitest.Response{Status: 204})
	if stdout, stderr, code := h.run("rename", "pets", "0a1b2c3d", "--template", "welcome"); code != apikit.ExitOK || stdout != "Renamed 0a1b2c3d to Rex\n" {
		t.Errorf("a call from a template: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestCompleteLookups(t *testing.T) {
	h := groomer(t)
	h.api.Handle("GET", adminAPI+"/owners?city=Linz&limit=100&skip=0", apitest.Response{Body: raw(`[
		{"id": "11111111", "name": "Ada", "number": 7},
		{"id": "22222222", "name": "Abe", "number": 8},
		{"id": "33333333", "name": "Bob", "number": 9}]`)})

	offers, _ := complete(t, h, "--token", "secret", "rename", "pets", "0a1b2c3d", "--vet", "A")
	if !slices.Equal(offers, []string{"Abe", "Ada"}) {
		t.Errorf("lookup = %v", offers)
	}

	// Without a token nothing is asked of the API, and nobody is asked for
	// a password.
	quiet := groomer(t)
	quiet.options.UI = answers{}
	if offers, _ = complete(t, quiet, "rename", "pets", "0a1b2c3d", "--vet", ""); len(offers) != 0 {
		t.Errorf("lookup without a token = %v", offers)
	}
	if sent := quiet.api.Requests(); len(sent) != 0 {
		t.Errorf("a completion without a token sent %d requests", len(sent))
	}
}
