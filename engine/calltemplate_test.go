package engine_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

// writeFile writes a file for a test and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// resolvedParams returns the values a dry run resolved, by parameter.
func resolvedParams(t *testing.T, result *engine.CallResult, err error) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	values := map[string]any{}
	for _, param := range result.DryRun {
		values[param.Name] = param.Value
	}
	return values
}

// bookDry checks a booking for Rex without sending it. in adds to the
// parameters --pet and --role, which every booking of the tests has.
func bookDry(t *testing.T, shop *shop, in engine.CallInput) (*engine.CallResult, error) {
	t.Helper()
	params := map[string]any{"pet": rex, "role": "keeper"}
	maps.Copy(params, in.Params)
	in.Resource, in.Call, in.Params, in.DryRun = "appointments", "book", params, true
	return shop.Apply(t.Context(), in)
}

func TestReadSource(t *testing.T) {
	t.Run("a file, without the line breaks at its end", func(t *testing.T) {
		shop := callShop(t, nil)
		path := writeFile(t, "body.txt", "Hello ${pet}\n\n  second line \r\n\n")
		text, err := shop.ReadSource(t.Context(), path, "--text-template-file")
		if err != nil || text != "Hello ${pet}\n\n  second line \r" {
			t.Errorf("ReadSource = %q, %v", text, err)
		}
	})

	t.Run("a file in the home directory", func(t *testing.T) {
		home := t.TempDir()
		// Windows names the home directory in another variable.
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		if err := os.WriteFile(filepath.Join(home, "body.txt"), []byte("from home"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		shop := callShop(t, nil)
		text, err := shop.ReadSource(t.Context(), "~/body.txt", "--text-template-file")
		if err != nil || text != "from home" {
			t.Errorf("ReadSource = %q, %v", text, err)
		}
		// Only a ~ of its own is the home directory.
		_, err = shop.ReadSource(t.Context(), "~body.txt", "--text-template-file")
		wantError(t, err, apikit.KindFailed, "unable to read --text-template-file ~body.txt: ")
	})

	t.Run("a file that cannot be read", func(t *testing.T) {
		shop := callShop(t, nil)
		missing := filepath.Join(t.TempDir(), "missing.txt")
		_, err := shop.ReadSource(t.Context(), missing, "--text-template-file")
		wantError(t, err, apikit.KindFailed, "unable to read --text-template-file "+missing+": ")
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the error %v does not wrap the cause", err)
		}

		_, err = shop.ReadSource(t.Context(), writeFile(t, "latin1.txt", "Gr\xfc\xdfe"), "template")
		wantError(t, err, apikit.KindFailed, "unable to read template ", "it is no UTF-8 text")
	})

	t.Run("a URL, fetched without the credential", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", "https://beds.example.test/body.txt?v=2", apitest.Response{Raw: "Für ${role}\n"})
		shop.api.Handle("GET", "HTTP://beds.example.test/gone.txt", apitest.Response{Status: 404, Raw: "gone"})
		shop.api.Handle("GET", "https://beds.example.test/down.txt", apitest.Response{Err: errors.New("connection refused")})

		text, err := shop.ReadSource(t.Context(), "https://beds.example.test/body.txt?v=2", "--text-template-file")
		if err != nil || text != "Für ${role}" {
			t.Errorf("ReadSource = %q, %v", text, err)
		}
		_, err = shop.ReadSource(t.Context(), "HTTP://beds.example.test/gone.txt", "--text-template-file")
		wantError(t, err, apikit.KindFailed, "unable to read --text-template-file HTTP://beds.example.test/gone.txt: the server answered with status 404")
		_, err = shop.ReadSource(t.Context(), "https://beds.example.test/down.txt", "--text-template-file")
		wantError(t, err, apikit.KindFailed, "unable to read --text-template-file https://beds.example.test/down.txt: ", "connection refused")

		_, err = shop.ReadSource(t.Context(), "http://[::1/body.txt", "--text-template-file")
		wantError(t, err, apikit.KindFailed, "unable to read --text-template-file http://[::1/body.txt: ")

		// Whoever serves the file is not told who asks.
		for _, request := range shop.api.Requests() {
			if len(request.Header) != 0 {
				t.Errorf("%s %s%s was sent with the headers %v, want none", request.Method, request.Host, request.Path, request.Header)
			}
		}
	})

	t.Run("standard input, once", func(t *testing.T) {
		shop := callShop(t, func(o *engine.Options) { o.Stdin = strings.NewReader("typed ${role}\n") })
		text, err := shop.ReadSource(t.Context(), "-", "--title-template-file")
		if err != nil || text != "typed ${role}" {
			t.Errorf("ReadSource = %q, %v", text, err)
		}
		// The second reader would get nothing and not know why.
		_, err = shop.ReadSource(t.Context(), "-", "--text-template-file")
		wantError(t, err, apikit.KindUsage, "only one of --title-template-file and --text-template-file can read stdin (-)")
		_, err = shop.ReadSource(t.Context(), "-", "--title-template-file")
		wantError(t, err, apikit.KindUsage, "only one --title-template-file can read stdin (-)")
		_, err = shop.LoadCallTemplate(t.Context(), "-")
		wantError(t, err, apikit.KindUsage, "only one of --title-template-file and --template can read stdin (-)")
	})

	t.Run("no standard input is an empty one", func(t *testing.T) {
		shop := callShop(t, nil)
		text, err := shop.ReadSource(t.Context(), "-", "--title-template-file")
		if err != nil || text != "" {
			t.Errorf("ReadSource = %q, %v, want nothing", text, err)
		}
		_, err = shop.ReadSource(t.Context(), "-", "--text-template-file")
		wantError(t, err, apikit.KindUsage, "can read stdin (-)")
	})
}

// shipped are the call templates of a tool, and what else may lie around.
var shipped = fstest.MapFS{
	"welcome.yaml":     {Data: []byte("resource: appointments\ncall: book\nparameters:\n  title: Welcome\n")},
	"farewell.yml":     {Data: []byte("parameters:\n  title: Goodbye\n")},
	"both.yaml":        {Data: []byte("parameters:\n  title: from yaml\n")},
	"both.yml":         {Data: []byte("parameters:\n  title: from yml\n")},
	"notes.txt":        {Data: []byte("no template")},
	"plain":            {Data: []byte("no template")},
	"more/hidden.yaml": {Data: []byte("parameters: {}\n")},
}

func TestBuiltinTemplates(t *testing.T) {
	shop := callShop(t, func(o *engine.Options) { o.Templates = shipped })
	if got, want := shop.BuiltinTemplates(), []string{"both", "farewell", "welcome"}; !slices.Equal(got, want) {
		t.Errorf("BuiltinTemplates() = %q, want %q", got, want)
	}
	if got := callShop(t, nil).BuiltinTemplates(); got != nil {
		t.Errorf("BuiltinTemplates() = %q without templates, want none", got)
	}
}

func TestLoadCallTemplate(t *testing.T) {
	const document = `
resource: appointment
call: book
variables:
  guest: friend
  count: 2
  beds:
parameters:
  role: keeper
  title: Dear ${guest}
  internal: yes
  on: on
  date: 2026-01-31
  number: 007
  list: [a, 1, true]
`
	check := func(t *testing.T, template *engine.CallTemplate, err error, origin string) {
		t.Helper()
		if err != nil {
			t.Fatalf("LoadCallTemplate: %v", err)
		}
		if template.Resource != "appointment" || template.Call != "book" || template.Origin != origin {
			t.Errorf("template for %q %q from %q, want appointment, book and %q", template.Resource, template.Call, template.Origin, origin)
		}
		wantJSON(t, template.Variables, `{"guest":"friend","count":2,"beds":null}`)
		// Read as YAML 1.2: yes, on and a date are texts.
		wantJSON(t, template.Parameters, `{"role":"keeper","title":"Dear ${guest}","internal":"yes","on":"on","date":"2026-01-31","number":7,"list":["a",1,true]}`)
	}

	t.Run("from a file", func(t *testing.T) {
		shop := callShop(t, nil)
		path := writeFile(t, "booking.yaml", document)
		template, err := shop.LoadCallTemplate(t.Context(), path)
		check(t, template, err, "--template "+path)
	})
	t.Run("from a URL", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", "https://beds.example.test/booking.yaml", apitest.Response{Raw: document})
		template, err := shop.LoadCallTemplate(t.Context(), "https://beds.example.test/booking.yaml")
		check(t, template, err, "--template https://beds.example.test/booking.yaml")
		if got := authorizations(shop.api); !slices.Equal(got, []string{""}) {
			t.Errorf("credentials sent: %q, want none", got)
		}
	})
	t.Run("from standard input", func(t *testing.T) {
		shop := callShop(t, func(o *engine.Options) { o.Stdin = strings.NewReader(document) })
		template, err := shop.LoadCallTemplate(t.Context(), "-")
		check(t, template, err, "--template -")
	})

	t.Run("a template the tool ships, by its name", func(t *testing.T) {
		shop := callShop(t, func(o *engine.Options) { o.Templates = shipped })
		for source, title := range map[string]string{
			"welcome":      "Welcome",
			"welcome.yaml": "Welcome",
			"farewell":     "Goodbye",
			"farewell.yml": "Goodbye",
			"both":         "from yaml",
			"both.yml":     "from yml",
		} {
			template, err := shop.LoadCallTemplate(t.Context(), source)
			if err != nil {
				t.Fatalf("LoadCallTemplate(%s): %v", source, err)
			}
			if got, _ := template.Parameters.Get("title"); got != title || template.Origin != "--template "+source {
				t.Errorf("LoadCallTemplate(%s) has the title %v from %q, want %s", source, got, template.Origin, title)
			}
		}
		// A document that says nothing about its call is for any.
		if template, _ := shop.LoadCallTemplate(t.Context(), "farewell"); template.Resource != "" || template.Call != "" || template.Variables.Len() != 0 {
			t.Errorf("farewell is for %q %q with %d variables, want any call and none", template.Resource, template.Call, template.Variables.Len())
		}
	})

	t.Run("a source that names nothing", func(t *testing.T) {
		shop := callShop(t, func(o *engine.Options) { o.Templates = shipped })
		for _, source := range []string{"nope", "notes", "notes.txt", "plain", "more/hidden", "more/hidden.yaml", "hidden", ""} {
			_, err := shop.LoadCallTemplate(t.Context(), source)
			wantError(t, err, apikit.KindUsage, "--template "+source+" is no file, URL or built-in template (built-in: both, farewell, welcome)")
		}
		_, err := callShop(t, nil).LoadCallTemplate(t.Context(), "welcome")
		wantError(t, err, apikit.KindUsage, "--template welcome is no file, URL or built-in template (built-in: none)")
	})

	t.Run("a file comes before a template of the tool", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if err := os.WriteFile("welcome", []byte("parameters:\n  title: from the file\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		shop := callShop(t, func(o *engine.Options) { o.Templates = shipped })
		template, err := shop.LoadCallTemplate(t.Context(), "welcome")
		if err != nil {
			t.Fatalf("LoadCallTemplate: %v", err)
		}
		wantJSON(t, template.Parameters, `{"title":"from the file"}`)
	})

	mistakes := map[string]struct {
		document string
		message  string
	}{
		"keys a call template does not have":  {"name: x\nshade: red\nparameters: {}\n", " has unknown keys name, shade; known are resource, call, variables, parameters"},
		"a list":                              {"- x\n", " must be a mapping with 'parameters'"},
		"nothing":                             {"", " must be a mapping with 'parameters'"},
		"no YAML":                             {"parameters: {\n", " is no valid YAML: "},
		"parameters that are a list":          {"parameters: [x]\n", ": 'parameters' must map parameter names to values"},
		"parameters that are a text":          {"parameters: a\n", ": 'parameters' must map parameter names to values"},
		"variables that are a list":           {"variables: [x]\n", ": 'variables' must map variable names to a default value, or to nothing for a variable without one"},
		"a variable with a flag as default":   {"variables: {a: true}\n", ": 'variables' must map variable names"},
		"a variable with a list as default":   {"variables: {a: [b]}\n", ": 'variables' must map variable names"},
		"a variable with a name that is none": {"variables: {a.b: c}\n", ": 'variables' must map variable names"},
	}
	for name, test := range mistakes {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			path := writeFile(t, "wrong.yaml", test.document)
			_, err := shop.LoadCallTemplate(t.Context(), path)
			wantError(t, err, apikit.KindUsage, "--template "+path+test.message)
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("the error takes more than one line: %q", err)
			}
		})
	}

	t.Run("a file that cannot be read", func(t *testing.T) {
		shop := callShop(t, nil)
		_, err := shop.LoadCallTemplate(t.Context(), t.TempDir())
		wantError(t, err, apikit.KindFailed, "unable to read --template ")
	})
}

func TestTemplatesFillParameters(t *testing.T) {
	const byTag = adminAPI + "/pets?search=%22REX-7%22"

	t.Run("a template sees the parameters as given, and the variables", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})

		result, err := bookDry(t, shop, engine.CallInput{
			Params: map[string]any{"pet": "REX-7", "room-id": "0a0a0a03"},
			Templates: map[string]engine.Text{
				"title": {Value: "Visit of ${pet} for ${role}s in ${room}", Origin: "--title-template"},
				"text":  {Value: "${file_list}|${file-list}|$$9 $${pet} $pet $$PATH", Origin: "--text-template-file body.txt"},
			},
			Vars: []engine.Var{{Name: "file-list", Value: "a\n${pet}"}},
		})
		got := resolvedParams(t, result, err)
		// The tag that was given, not the ID it stands for. A name is
		// written with dashes or underscores. A value is filled in once:
		// what it holds is no placeholder.
		if got["title"] != "Visit of REX-7 for keepers in 0a0a0a03" || got["text"] != "a\n${pet}|a\n${pet}|$9 ${pet} REX-7 $PATH" {
			t.Errorf("title = %q, text = %q", got["title"], got["text"])
		}
		if got["pet"] != rex {
			t.Errorf("pet = %q, want the ID it was resolved to after the templates", got["pet"])
		}
	})

	t.Run("a parameter that is given as it is keeps its placeholders", func(t *testing.T) {
		shop := callShop(t, nil)
		result, err := bookDry(t, shop, engine.CallInput{Params: map[string]any{"title": "Leave ${pet} and $$", "text": "Leave ${y}"}})
		got := resolvedParams(t, result, err)
		if got["title"] != "Leave ${pet} and $$" || got["text"] != "Leave ${y}" {
			t.Errorf("title = %q, text = %q, want them as given", got["title"], got["text"])
		}
	})

	t.Run("the fields of the item a parameter names", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
		shop.api.Handle("GET", petURL, apitest.Response{Raw: `{"id":"` + rex + `","tag":"REX-7","name":"Rex","chipped":false,
			"colors":["black","tan",3,true],"mixed":[1,{"a":2}],"chip":{"code":"c-1"},"none":[],"nothing":null}`})

		result, err := bookDry(t, shop, engine.CallInput{
			Params: map[string]any{"pet": "REX-7", "text": "Soon."},
			Templates: map[string]engine.Text{"title": {
				Value: "${pet.name} (${pet.tag}), chipped ${pet.chipped}, ${pet.colors}, ${pet.mixed}, ${pet.chip}, ${pet.chip.code}, [${pet.none}], $${pet.id}",
			}},
		})
		got := resolvedParams(t, result, err)
		if want := `Rex (REX-7), chipped false, black, tan, 3, true, [1, {"a": 2}], {"code": "c-1"}, c-1, [], ${pet.id}`; got["title"] != want {
			t.Errorf("title = %q\nwant    %q", got["title"], want)
		}
		// The pet is found by its tag and fetched once, however many
		// fields are asked for.
		wantRequests(t, shop.api, "GET "+byTag, "GET "+petURL)
	})

	t.Run("the ID as it was given", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", byTag, apitest.Response{Raw: `[{"id":"` + rex + `","tag":"REX-7"}]`})
		shop.api.Handle("PATCH", "/v1/pets/"+rex, noContent)

		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: "REX-7", Call: "rename",
			Templates: map[string]engine.Text{"name": {Value: "Was ${id}"}}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api, "GET "+byTag, "PATCH "+mainAPI+"/pets/"+rex+` {"name":"Was REX-7"}`)
	})

	t.Run("what a template yields is checked like a value", func(t *testing.T) {
		shop := callShop(t, nil)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "rename",
			Templates: map[string]engine.Text{"name": {Value: "${first} ${last}"}},
			Vars:      []engine.Var{{Name: "first", Value: "Rex"}, {Name: "last", Value: "the twenty-first!"}}})
		wantError(t, err, apikit.KindUsage, "--name has 21 characters, at most 20 are possible")
		wantRequests(t, shop.api)
	})
}

func TestTemplatesNeedEveryValue(t *testing.T) {
	tests := map[string]struct {
		in      engine.CallInput
		message string
	}{
		"a variable nobody gave": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${beds} ${x.y} ${beds}", Origin: "--text-template"}}},
			"--text-template uses ${beds}, ${x.y} without a value; give --template-var beds=VALUE, give --template-var x.y=VALUE",
		},
		// A plain $name without a value would reach the reader as it is.
		"a plain name": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "$USER or ${USER}", Origin: "--text-template"}}},
			"--text-template uses ${USER}, $USER without a value; give --template-var USER=VALUE; $$ is a literal $",
		},
		"another template, and a parameter that was not given": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${title} ${vet} ${room-id}", Origin: "--text-template-file body.txt"}}},
			"--text-template-file body.txt uses ${title}, ${vet}, ${room-id} without a value; no template can use title, give --vet, give --room",
		},
		"a template that uses itself": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${text}"}}},
			"--text-template uses ${text} without a value; no template can use text",
		},
		"a flag": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "internal": true}, Templates: map[string]engine.Text{"text": {Value: "${internal}"}}},
			"--text-template uses ${internal} without a value; give --internal",
		},
		"the ID of a call that takes none": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${id}"}}},
			"--text-template uses ${id} without a value; book takes no ID",
		},
		"a field of an item whose parameter was not given": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${owner.name}"}}},
			"--text-template uses ${owner.name} without a value; give --owner",
		},
		"a field of what is no item": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${role.name}"}}},
			"--text-template uses ${role.name} without a value; give --template-var role.name=VALUE",
		},
		"a variable that no template uses": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${a}"}},
				Vars: []engine.Var{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}},
			"template variable b: no template uses it",
		},
		"variables that no template uses": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "${b} ${c}"}, Vars: []engine.Var{{Name: "b", Value: "2"}, {Name: "c", Value: "3"}}},
			"template variable b, c: no template uses them",
		},
		"a variable that is given twice": {
			engine.CallInput{Params: map[string]any{"title": "Visit"}, Templates: map[string]engine.Text{"text": {Value: "${a-b}"}},
				Vars: []engine.Var{{Name: "a-b", Value: "1"}, {Name: "a_b", Value: "2", Origin: "--template-var-file"}}},
			"template variable a_b is given more than once",
		},
		"a variable named like a parameter": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, Vars: []engine.Var{{Name: "pet", Value: "x"}}},
			"--template-var pet: pet is a parameter, give --pet",
		},
		"a variable named like the alias of a parameter": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, Vars: []engine.Var{{Name: "room_id", Value: "x", Origin: "--template-var-file"}}},
			"--template-var-file room_id: room_id is a parameter, give --room",
		},
		"a variable named like the ID": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, Vars: []engine.Var{{Name: "id", Value: "x"}}},
			"--template-var id: id is the ID of the call",
		},
		"a variable whose name is none": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, Vars: []engine.Var{{Name: "1a", Value: "x"}}},
			"--template-var 1a: a variable is named with letters, digits, - and _",
		},
		"a template and a value for one parameter": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, Templates: map[string]engine.Text{"text": {Value: "Later.", Origin: "--text-template-file body.txt"}}},
			"--text-template-file body.txt cannot be given together with --text",
		},
		"a template for a parameter that takes none": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, Templates: map[string]engine.Text{"room-id": {Value: "x"}}},
			"--room-template: --room takes no template",
		},
		"a template for a parameter the call does not have": {
			engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, Templates: map[string]engine.Text{"color": {Value: "x"}}},
			"book has no parameter color; it has pet, owner, vet, role, title, text, room, internal",
		},
		"a required parameter that neither a value nor a template gives": {
			engine.CallInput{Templates: map[string]engine.Text{"text": {Value: "Later."}}},
			"book needs --title/--title-template/--title-template-file",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			result, err := bookDry(t, shop, test.in)
			wantError(t, err, apikit.KindUsage, test.message)
			if err.Error() != test.message || result != nil {
				t.Errorf("error %q, result %+v, want %q and no result", err, result, test.message)
			}
			wantRequests(t, shop.api)
		})
	}

	t.Run("a field the item does not have", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", petURL, apitest.Response{Raw: `{"id":"` + rex + `","nothing":null}`})
		_, err := bookDry(t, shop, engine.CallInput{Params: map[string]any{"text": "Soon."},
			Templates: map[string]engine.Text{"title": {Value: "${pet.nope} ${pet.nothing} ${pet.a..b} ${owner.name}"}}})
		wantError(t, err, apikit.KindUsage,
			"--title-template uses ${pet.nope}, ${pet.nothing}, ${pet.a..b}, ${owner.name} without a value; "+
				"the pet has no nope, the pet has no nothing, the pet has no a..b, give --owner")
	})

	t.Run("an item that cannot be fetched", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", petURL, apitest.Response{Status: 404})
		_, err := bookDry(t, shop, engine.CallInput{Params: map[string]any{"text": "Soon."},
			Templates: map[string]engine.Text{"title": {Value: "${pet.name} ${pet.tag}"}}})
		wantError(t, err, apikit.KindNotFound, "failed to fetch pet by ID "+rex+": 404")
		wantRequests(t, shop.api, "GET "+petURL)
	})

	t.Run("a call that needs its ID for a template", func(t *testing.T) {
		shop := callShop(t, nil)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", Call: "rename", Templates: map[string]engine.Text{"name": {Value: "${id}"}}})
		wantError(t, err, apikit.KindUsage, "--name-template uses ${id} without a value; give the ID of the pet")
	})

	t.Run("two templates for one parameter", func(t *testing.T) {
		shop := callShop(t, nil, `
resources:
  toys:
    setters:
      - name: label
        payloadTemplate: {label: "${label}"}
        parameters:
          - {name: label, aliases: [name], template: true}
`)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "label",
			Templates: map[string]engine.Text{"label": {Value: "a"}, "name": {Value: "b", Origin: "--name-template-file b.txt"}}})
		wantError(t, err, apikit.KindUsage, "--name-template-file b.txt: --label has a template already")
	})

	t.Run("variables for a call without templates", func(t *testing.T) {
		shop := callShop(t, nil)
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "archive", Vars: []engine.Var{{Name: "a", Value: "1"}}})
		wantError(t, err, apikit.KindUsage, "template variable a: no template uses it")
		wantRequests(t, shop.api)
	})
}

func TestCallTemplateGivesParameters(t *testing.T) {
	const carersOfRex = mainAPI + "/pets/" + rex + "/carers?active=true&limit=100&skip=0"
	load := func(t *testing.T, shop *shop, document string) *engine.CallTemplate {
		t.Helper()
		template, err := shop.LoadCallTemplate(t.Context(), writeFile(t, "booking.yaml", document))
		if err != nil {
			t.Fatalf("LoadCallTemplate: %v", err)
		}
		return template
	}
	const booking = `
resource: appointment
call: book
variables:
  guest: friend
  count: 2
  beds:
parameters:
  role: sitter
  title: Dear ${guest}, ${count} beds
  text: "List: ${beds}"
  internal: true
  room-id: 0a0a0a03
`

	t.Run("parameters, templates and the defaults of variables", func(t *testing.T) {
		shop := callShop(t, nil)
		result, err := bookDry(t, shop, engine.CallInput{
			Params: map[string]any{"role": nil}, File: load(t, shop, booking), Vars: []engine.Var{{Name: "beds", Value: "a.txt"}}})
		got := resolvedParams(t, result, err)
		wantJSON(t, []any{got["role"], got["title"], got["text"], got["internal"], got["room"]},
			`["sitter","Dear friend, 2 beds","List: a.txt",true,"0a0a0a03"]`)

		// A variable that is given replaces the default.
		result, err = bookDry(t, shop, engine.CallInput{
			Params: map[string]any{"role": nil}, File: load(t, shop, booking),
			Vars: []engine.Var{{Name: "beds", Value: "a.txt"}, {Name: "guest", Value: "all"}, {Name: "count", Value: "no"}}})
		if got := resolvedParams(t, result, err); got["title"] != "Dear all, no beds" {
			t.Errorf("title = %q, want the variables that were given", got["title"])
		}
	})

	t.Run("what is given directly wins", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("GET", carersOfRex, apitest.Response{Raw: carers})
		result, err := bookDry(t, shop, engine.CallInput{
			File:      load(t, shop, booking),
			Params:    map[string]any{"role": nil, "vet": "ada@example.test", "room": "0a0a0a04"},
			Templates: map[string]engine.Text{"text": {Value: "Mine, ${guest}"}},
		})
		got := resolvedParams(t, result, err)
		// --vet and --role exclude each other, so the role of the
		// template goes; the text given as a template replaces that of
		// the file, and the variable the file wanted for it is not
		// needed.
		if _, hasRole := got["role"]; hasRole || got["vet"] != ada || got["room"] != "0a0a0a04" || got["text"] != "Mine, friend" || got["title"] != "Dear friend, 2 beds" {
			t.Errorf("resolved %v", got)
		}
	})

	t.Run("a template made by hand", func(t *testing.T) {
		shop := callShop(t, nil)
		result, err := bookDry(t, shop, engine.CallInput{Params: map[string]any{"title": "Visit", "text": "Soon."}, File: &engine.CallTemplate{}})
		if got := resolvedParams(t, result, err); got["title"] != "Visit" {
			t.Errorf("title = %q", got["title"])
		}
	})

	t.Run("a template for any call that has its parameters", func(t *testing.T) {
		shop := callShop(t, nil)
		shop.api.Handle("PATCH", "/v1/pets/"+rex, noContent)
		template := load(t, shop, "call: set\nparameters: {diet: hay, key: color, value: black}\n")
		// The alias names a part of the call the template is for.
		_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "set", File: template})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if got := len(sent(shop.api)); got != 2 {
			t.Errorf("%d requests sent, want one for each of the two setters the template gives parameters of", got)
		}
		_, err = shop.Apply(t.Context(), engine.CallInput{Resource: "pets", ID: rex, Call: "trait", File: load(t, shop, "call: set\nparameters: {key: color, value: black}\n")})
		if err != nil {
			t.Fatalf("Apply with the alias: %v", err)
		}
	})

	mistakes := map[string]struct {
		document string
		params   map[string]any
		message  string
	}{
		"a template for another resource": {"resource: owners\nparameters: {}\n", nil, " is for owners, not appointments"},
		"a template for another call":     {"call: add\nparameters: {}\n", nil, " is for add, not book"},
		"a parameter the call does not have": {
			"parameters:\n  shade: red\n", nil,
			": book has no parameter shade; it has pet, owner, vet, role, title, text, room, internal",
		},
		"a parameter and its alias":             {"parameters:\n  room: a\n  room-id: b\n", nil, " gives room twice"},
		"parameters that exclude each other":    {"parameters:\n  role: keeper\n  vet: ada@example.test\n", map[string]any{"role": nil}, " gives --role, --vet, which exclude each other"},
		"a text for a flag":                     {"parameters:\n  internal: 'on'\n", nil, ": internal must be true or false"},
		"a list for a template":                 {"parameters:\n  title: [x, y]\n", nil, ": title must be a text"},
		"a number for a template":               {"parameters:\n  title: 5\n", nil, ": title must be a text"},
		"a list for a text":                     {"parameters:\n  room: [a]\n", nil, ": room must be of type str"},
		"a mapping for a text":                  {"parameters:\n  room: {a: b}\n", nil, ": room must be of type str"},
		"nothing for a text":                    {"parameters:\n  room:\n", nil, ": room must be of type str"},
		"a variable without a default or value": {"variables:\n  beds:\nparameters:\n  title: Visit\n  text: ${beds}\n", nil, " uses ${beds} without a value; give --template-var beds=VALUE"},
		"a required parameter it does not give": {"parameters:\n  title: Visit\n", nil, "book needs --text/--text-template/--text-template-file"},
	}
	for name, test := range mistakes {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil)
			template := load(t, shop, test.document)
			_, err := bookDry(t, shop, engine.CallInput{File: template, Params: test.params})
			wantError(t, err, apikit.KindUsage, test.message)
			if !strings.Contains(err.Error(), template.Origin) && !strings.HasPrefix(err.Error(), "book needs") {
				t.Errorf("error %q does not name %s", err, template.Origin)
			}
			wantRequests(t, shop.api)
		})
	}

	t.Run("the values of a template take the type of their parameter", func(t *testing.T) {
		shop := callShop(t, nil, payloadOverlay)
		shop.api.Handle("PUT", "/v1/toys/"+toy+"/stamp", noContent)
		template := load(t, shop, "parameters:\n  label: 7\n  count: '12'\n  ratio: 2\n  loud: false\n  tags: [a, 1, 2.5]\n  note: yes\n")
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "stamp", File: template}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// A flag that is false was not given.
		wantRequests(t, shop.api, "PUT "+mainAPI+"/toys/"+toy+`/stamp {"label":"7","count":12,"ratio":2,"tags":["a","1","2.5"],"note":"say \"yes\" for $1",`+
			`"literal":"${count} {} []","braces":{"in":"a } and a { and a \\"}}`)

		for document, message := range map[string]string{
			"parameters: {label: a, count: 1.5}\n":             ": count must be of type int",
			"parameters: {label: a, count: true}\n":            ": count must be of type int",
			"parameters: {label: a, count: 1, ratio: .nan}\n":  ": ratio must be of type float",
			"parameters: {label: a, count: 1, tags: []}\n":     ": tags must be a value or a list of values",
			"parameters: {label: a, count: 1, tags: [[a]]}\n":  ": tags must be a value or a list of values",
			"parameters: {label: a, count: 1, tags: [true]}\n": ": tags must be a value or a list of values",
		} {
			_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "stamp", File: load(t, shop, document)})
			wantError(t, err, apikit.KindUsage, message)
		}
	})
}
