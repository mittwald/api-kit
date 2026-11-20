package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
)

// posting gives the pet shop a call that posts about a pet with photos at
// a time, and checks the text of the post.
const posting = `
resources:
  pets:
    setters:
      - name: post
        pathTemplate: pets/${id}/posts
        payloadTemplate: {text: "${text}", photos: "${photo}", at: "${at}"}
        parameters:
          - {name: text, format: markdown}
          - {name: photo, type: file, list: true, required: false, help: Photo to show}
          - {name: at, type: time, required: false, timezone: Europe/Berlin, presets: {soon: "+1h"}, help: When to post}
`

// poster returns a tool for the pet shop that checks markdown.
func poster(t *testing.T) *tool {
	t.Helper()
	h := newTool(t)
	h.options.Formats = map[string]func(string) []string{"markdown": func(value string) []string {
		if strings.Contains(value, "<") {
			return []string{"HTML is shown as text"}
		}
		return nil
	}}
	h.overlay(posting)
	return h
}

func TestCallWarnsOfWhatAFormatFinds(t *testing.T) {
	h := poster(t)
	h.api.Handle("POST", mainAPI+"/pets/0a1b2c3d/posts", apitest.Response{Status: 204})

	stdout, stderr, code := h.run("post", "pets", "0a1b2c3d", "--text", "<b>Rex</b>")
	if code != apikit.ExitOK || !strings.HasPrefix(stdout, "Successfully applied") || stderr != "WARNING: --text: HTML is shown as text\n" {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestCallTakesSeveralFiles(t *testing.T) {
	h := poster(t)
	h.api.Handle("POST", mainAPI+"/pets/0a1b2c3d/posts", apitest.Response{Status: 204})
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	_, stderr, code := h.run("post", "pets", "0a1b2c3d", "--text", "Rex", "--photo", first, "--photo", second)
	if code != apikit.ExitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	// A path is a JSON string, with the backslashes of Windows escaped.
	quoted := func(path string) string {
		data, _ := json.Marshal(path)
		return string(data)
	}
	if got, want := lastBody(t, h), `{"text": "Rex", "photos": [`+quoted(first)+`, `+quoted(second)+`]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}

	_, stderr, code = h.run("post", "pets", "0a1b2c3d", "--text", "Rex", "--photo", filepath.Join(dir, "c.png"))
	if code != apikit.ExitUsage || !strings.Contains(stderr, "c.png: no such file") {
		t.Errorf("missing file: exit code = %d, stderr = %q", code, stderr)
	}
}

func TestTimeParameterSaysWhatItTakes(t *testing.T) {
	h := poster(t)

	stdout, _, code := h.run("post", "pets", "--help")
	if code != apikit.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"When to post (an ISO 8601 time", "or one of soon; in Europe/Berlin)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
	stdout, _, _ = h.run("__complete", "post", "pets", "0a1b2c3d", "--at", "")
	if !strings.HasPrefix(stdout, "soon\nnow\ntoday\ntomorrow\n") {
		t.Errorf("completion of --at = %q", stdout)
	}
}

func TestDefinitionMayNameOnlyFormatsOfTheTool(t *testing.T) {
	h := newTool(t)
	h.overlay(posting)

	_, stderr, code := h.run("post", "pets", "0a1b2c3d", "--text", "Rex")
	if code != apikit.ExitFailed || !strings.Contains(stderr, `format: unknown format "markdown", the tool checks: `) {
		t.Errorf("exit code = %d, stderr = %q", code, stderr)
	}
}
