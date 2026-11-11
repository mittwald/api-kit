package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mittwald/cli-kit/buildinfo"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/auth"
	"github.com/mittwald/api-kit/cli"
	"github.com/mittwald/api-kit/definition"
)

// The base URLs of the pet shop.
const (
	mainAPI    = "https://api.example.test/v1"
	adminAPI   = "https://admin.example.test/v1"
	stagingAPI = "https://admin.staging.example.test/v1"
)

// tool is a command-line tool for the pet shop of package apitest, wired to
// a fake API.
type tool struct {
	t       *testing.T
	api     *apitest.Server
	options cli.Options
	env     map[string]string
}

// newTool returns the tool. Its runs authenticate with the token "secret"
// unless a test passes another or none.
func newTool(t *testing.T) *tool {
	t.Helper()
	api := apitest.New(t)
	h := &tool{t: t, api: api, env: map[string]string{}}
	h.options = cli.Options{
		Name:       "shop",
		Short:      "Query and change the pet shop",
		Build:      buildinfo.Info{Version: "v1.2.3"},
		Definition: definition.Source{Name: "petshop.yaml", Data: []byte(apitest.PetShop)},
		EnvPrefix:  "SHOP",
		Store:      memory{},
		HTTP:       api.Client(),
		Getenv:     func(name string) string { return h.env[name] },
	}
	return h
}

// run runs the tool with the token "secret" and returns what it wrote and
// its exit code.
func (h *tool) run(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	return h.runBare(append([]string{"--token", "secret"}, args...)...)
}

// runBare runs the tool with exactly these arguments.
func (h *tool) runBare(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	var out, errOut bytes.Buffer
	options := h.options
	options.Streams = cli.Streams{In: strings.NewReader(""), Out: &out, Err: &errOut}
	code = cli.New(options).Execute(context.Background(), args)
	return out.String(), errOut.String(), code
}

// overlay writes a definition file for the user and makes the tool read it.
func (h *tool) overlay(content string) string {
	h.t.Helper()
	path := filepath.Join(h.t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.options.Overlays = []cli.Overlay{{Path: path, Optional: true}}
	return path
}

// raw is a response body given as JSON text, so that the order of its keys
// is the one written.
func raw(text string) json.RawMessage { return json.RawMessage(text) }

// memory is a session store that forgets everything with the test.
type memory map[string]string

func (m memory) Get(account string) (string, error) {
	value, ok := m[account]
	if !ok {
		return "", auth.ErrNotFound
	}
	return value, nil
}

func (m memory) Set(account, value string) error {
	m[account] = value
	return nil
}

func (m memory) Delete(account string) error {
	if _, ok := m[account]; !ok {
		return auth.ErrNotFound
	}
	delete(m, account)
	return nil
}

// bare is a tool without a definition: it has only the commands every tool
// has.
func bare(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := cli.New(cli.Options{
		Name:    "example",
		Short:   "Query and change the Example API",
		Build:   buildinfo.Info{Version: "v1.2.3"},
		Streams: cli.Streams{In: strings.NewReader(""), Out: &out, Err: &errOut},
	})
	code = app.Execute(context.Background(), args)
	return out.String(), errOut.String(), code
}

func TestNoCommandShowsHelpAndFails(t *testing.T) {
	stdout, stderr, code := bare(t)
	if code != apikit.ExitFailed {
		t.Errorf("exit code = %d, want %d", code, apikit.ExitFailed)
	}
	if !strings.Contains(stdout, "Query and change the Example API") || !strings.Contains(stdout, "Available Commands:") {
		t.Errorf("stdout is not the help text:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want it empty", stderr)
	}
}

func TestHelpFlagSucceeds(t *testing.T) {
	stdout, stderr, code := bare(t, "--help")
	if code != apikit.ExitOK || !strings.Contains(stdout, "Usage:") || stderr != "" {
		t.Errorf("exit code = %d, stderr = %q, stdout:\n%s", code, stderr, stdout)
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		stdout, _, code := bare(t, args...)
		if code != apikit.ExitOK || stdout != "example 1.2.3\n" {
			t.Errorf("%v: exit code = %d, stdout = %q", args, code, stdout)
		}
	}
	stdout, _, code := bare(t, "version")
	if code != apikit.ExitOK || !strings.HasPrefix(stdout, "example 1.2.3\n") {
		t.Errorf("version: exit code = %d, stdout = %q", code, stdout)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string // part of the message that names the offending word
		hint string
	}{
		{"unknown command", []string{"frobnicate"}, `"frobnicate"`, "Run 'example --help' for usage."},
		{"unknown flag", []string{"--frobnicate"}, "--frobnicate", "Run 'example --help' for usage."},
		{"unknown flag of a subcommand", []string{"version", "--frobnicate"}, "--frobnicate", "Run 'example version --help' for usage."},
		{"surplus argument", []string{"version", "extra"}, `"extra"`, "Run 'example version --help' for usage."},
		{"unknown shell", []string{"completion", "tcsh"}, `"tcsh"`, "Run 'example completion --help' for usage."},
		// Flags are parsed by the command that runs, so the hint names it
		// even when the flag belongs to the root.
		{"bad color mode", []string{"--color=sometimes", "version"}, "sometimes", "Run 'example version --help' for usage."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := bare(t, tt.args...)
			if code != apikit.ExitUsage {
				t.Errorf("exit code = %d, want %d", code, apikit.ExitUsage)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want it empty", stdout)
			}
			if !strings.HasPrefix(stderr, "ERROR: ") || !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr does not report %s:\n%s", tt.want, stderr)
			}
			if !strings.HasSuffix(stderr, tt.hint+"\n") {
				t.Errorf("stderr does not end with the hint %q:\n%s", tt.hint, stderr)
			}
		})
	}
}

func TestCompletionScripts(t *testing.T) {
	markers := map[string]string{
		"bash":       "__start_example",
		"zsh":        "#compdef example",
		"fish":       "complete -c example",
		"powershell": "Register-ArgumentCompleter",
	}
	for shell, marker := range markers {
		t.Run(shell, func(t *testing.T) {
			stdout, stderr, code := bare(t, "completion", shell)
			if code != apikit.ExitOK {
				t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stdout, marker) || stderr != "" {
				t.Errorf("the %s script lacks %q, or stderr is not empty: %q", shell, marker, stderr)
			}
		})
	}
}

func TestCompletionRequestOffersCommands(t *testing.T) {
	stdout, _, code := newTool(t).runBare("__complete", "")
	if code != apikit.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"get", "config", "version", "completion", "help"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("completion does not offer %q:\n%s", want, stdout)
		}
	}
}

func TestBuildNamesTheToolAfterTheProgram(t *testing.T) {
	root, err := cli.New(cli.Options{}).Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.Name() == "" {
		t.Error("the root command has no name")
	}
}

func TestConfigPrintsTheDefinition(t *testing.T) {
	h := newTool(t)
	stdout, stderr, code := h.runBare("config")
	if code != apikit.ExitOK || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "environments:\n  production:\n    apis:\n      main: https://api.example.test/v1\n") {
		t.Errorf("config does not print the definition as YAML, in its order:\n%s", stdout)
	}

	stdout, _, code = h.runBare("config", "-o", "json")
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || code != apikit.ExitOK {
		t.Fatalf("config -o json: exit code %d, %v", code, err)
	}
	if _, ok := document["resources"].(map[string]any)["pets"]; !ok {
		t.Errorf("the JSON document lacks the pets resource: %v", slicesOf(maps.Keys(document)))
	}

	_, stderr, code = h.runBare("config", "-o", "csv")
	if code != apikit.ExitUsage || !strings.Contains(stderr, "can be printed as yaml or json, not as csv") {
		t.Errorf("config -o csv: exit code %d, stderr %q", code, stderr)
	}
	if _, _, code = h.runBare("config", "extra"); code != apikit.ExitUsage {
		t.Errorf("config with an argument: exit code %d, want %d", code, apikit.ExitUsage)
	}
}

func TestOverlayExtendsTheDefinition(t *testing.T) {
	h := newTool(t)
	path := h.overlay("resources:\n  toys:\n    columns: [name]\n  pets:\n    columns: [name]\n")

	stdout, stderr, code := h.runBare("config")
	if code != apikit.ExitOK {
		t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
	}
	// The hint about the file goes to the error stream, so the output stays
	// a clean document.
	if !strings.Contains(stderr, "# A file of the same shape extends this definition: "+path) || strings.Contains(stdout, "#") {
		t.Errorf("stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "  toys:\n    columns:\n      - name\n") {
		t.Errorf("the overlay's resource is missing:\n%s", stdout)
	}
	// By default the file only adds: the columns of pets gain an entry.
	if !strings.Contains(stdout, "      - tag\n      - name\n      - species\n      - ownerId\n") {
		t.Errorf("the columns of pets were changed without --config-override:\n%s", stdout)
	}

	stdout, _, _ = h.runBare("config", "--config-override")
	if !strings.Contains(stdout, "  pets:\n    columns:\n      - name\n    filters:") {
		t.Errorf("--config-override did not replace the columns of pets:\n%s", stdout)
	}
	if stdout, _, _ = h.runBare("--config-override=false", "config"); !strings.Contains(stdout, "      - tag\n      - name\n") {
		t.Errorf("--config-override=false replaced the columns of pets:\n%s", stdout)
	}
	if stdout, _, _ = h.runBare("config", "--", "--config-override"); strings.Contains(stdout, "  pets:\n    columns:\n      - name\n    filters:") {
		t.Errorf("a word after -- was taken for the option")
	}
}

func TestOverlayProblems(t *testing.T) {
	h := newTool(t)
	h.overlay("resources:\n  pets:\n    shade: red\n    columns: tag\n")
	stdout, stderr, code := h.runBare("config")
	if code != apikit.ExitFailed || stdout != "" {
		t.Errorf("exit code = %d, stdout = %q", code, stdout)
	}
	// What an overlay cannot add is a warning; a mistake stops the run.
	if !strings.Contains(stderr, "WARNING: ") || !strings.Contains(stderr, "resources.pets.columns: ignored") {
		t.Errorf("stderr lacks the warning:\n%s", stderr)
	}
	if !strings.Contains(stderr, "ERROR: invalid definition in ") || !strings.Contains(stderr, "  - resources.pets.shade: unknown key") {
		t.Errorf("stderr lacks the problem:\n%s", stderr)
	}

	h.options.Overlays = []cli.Overlay{{Path: filepath.Join(t.TempDir(), "absent.yaml")}}
	_, stderr, code = h.runBare("config")
	if code != apikit.ExitFailed || !strings.Contains(stderr, "ERROR: cannot read definition") {
		t.Errorf("a required file that is absent: exit code %d, stderr %q", code, stderr)
	}
	h.options.Overlays[0].Optional = true
	if _, stderr, code = h.runBare("config"); code != apikit.ExitOK {
		t.Errorf("an optional file that is absent: exit code %d, stderr %q", code, stderr)
	}
}

// slicesOf collects a sequence into a slice.
func slicesOf(seq func(func(string) bool)) []string {
	var out []string
	seq(func(s string) bool { out = append(out, s); return true })
	return out
}
