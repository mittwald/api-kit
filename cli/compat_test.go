package cli_test

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/cli"
	"github.com/mittwald/api-kit/engine"
)

func TestOutputKeywordAndArgumentAsTwoWords(t *testing.T) {
	tests := []struct {
		args    []string
		want    string
		warning string
	}{
		{[]string{"get", "pets", "-o", "jsonpath", "$.name"}, "Rex\nTom\nAbe\n",
			"WARNING: -o jsonpath $.name is deprecated, use -o jsonpath=$.name\n"},
		{[]string{"get", "pets", "--output", "columns", "tag,age"}, "REX-7\t3\nTOM-2\t12\nABE-9\t5\n",
			"WARNING: --output columns tag,age is deprecated, use --output columns=tag,age\n"},
		{[]string{"-o", "jsonpath", "$.name", "get", "pets"}, "Rex\nTom\nAbe\n",
			"WARNING: -o jsonpath $.name is deprecated, use -o jsonpath=$.name\n"},
		// The spelling to use says nothing.
		{[]string{"get", "pets", "-o", "jsonpath=$.name"}, "Rex\nTom\nAbe\n", ""},
		{[]string{"get", "pets", "-o", "jsonraw", "--fail-empty"}, "", "WARNING: output format jsonraw is deprecated, use -o json-raw\n"},
	}
	for _, tt := range tests {
		h := newTool(t)
		listPets(h)
		stdout, stderr, code := h.run(tt.args...)
		if code != apikit.ExitOK || stderr != tt.warning || (tt.want != "" && stdout != tt.want) {
			t.Errorf("%v: exit code = %d, stdout = %q, stderr = %q", tt.args, code, stdout, stderr)
		}
	}
}

func TestFlagAliases(t *testing.T) {
	h := newTool(t)
	h.options.FlagAliases = []cli.FlagAlias{
		{Old: "api-main", New: "api", Value: "main", Silent: true},
		{Old: "staging", New: "env", Value: "staging"},
	}
	h.api.Handle("GET", mainAPI+"/pets?limit=100", apitest.Response{Body: raw(`[{"id": "0a1b2c3d", "name": "Main"}]`)})
	h.api.Handle("GET", stagingAPI+"/pets?limit=100", apitest.Response{Body: raw(`[{"id": "0a1b2c3d", "name": "Staged"}]`)})

	// A spelling that is as good as the new one says nothing.
	stdout, stderr, code := h.run("get", "pets", "--api-main", "-o", "name")
	if code != apikit.ExitOK || stdout != "Main\n" || stderr != "" {
		t.Errorf("a silent alias: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	// Another one names its replacement, once.
	stdout, stderr, code = h.run("--staging", "get", "pets", "--staging", "-o", "name")
	if code != apikit.ExitOK || stdout != "Staged\n" || stderr != "WARNING: --staging is deprecated, use --env=staging\n" {
		t.Errorf("an alias: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	stdout, _, _ = h.runBare("get", "--help")
	if strings.Contains(stdout, "api-main") || strings.Contains(stdout, "--staging") {
		t.Errorf("the help shows the older spellings:\n%s", stdout)
	}

	// The names of the aliases are taken: a parameter cannot have them.
	h.overlay("resources:\n  pets:\n    setters:\n      - {name: move, parameters: [{name: staging}]}\n")
	_, stderr, code = h.runBare("config")
	if code != apikit.ExitFailed || !strings.Contains(stderr, `option "staging" of call "move" is taken by an option every command has`) {
		t.Errorf("a parameter named like an alias: exit code = %d, stderr = %q", code, stderr)
	}
}

func TestHostCommands(t *testing.T) {
	h := newTool(t)
	// A command of the host that shows the oldest pet, the way the built-in
	// commands show a result.
	h.options.Commands = []cli.CommandFactory{func(env *cli.Env) *cobra.Command {
		return &cobra.Command{
			Use:   "oldest",
			Short: "Show the oldest pet",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				shop, err := env.Engine()
				if err != nil {
					return err
				}
				result, err := shop.Get(cmd.Context(), engine.GetRequest{Resource: "pets"})
				if err != nil {
					return err
				}
				items := result.Items()
				if env.Definition().Resource("pets").Columns == nil {
					t.Error("the definition is not there when the command runs")
				}
				return env.Render(cmd, &engine.Result{Resource: "pets", Value: items[1]})
			},
		}
	}}
	listPets(h)

	stdout, stderr, code := h.run("oldest")
	if code != apikit.ExitOK || stdout != "TAG    NAME  SPECIES  OWNERID\nTOM-2  Tom   cat      22222222\n" {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout, _, _ = h.run("oldest", "-o", "name"); stdout != "Tom\n" {
		t.Errorf("-o name: stdout = %q", stdout)
	}
	// One run has one engine: the listing was fetched once per run.
	if sent := h.api.Requests(); len(sent) != 2 {
		t.Errorf("%d requests for two runs, want 2", len(sent))
	}
	if stdout, _, _ = h.runBare("--help"); !strings.Contains(stdout, "  oldest      Show the oldest pet") {
		t.Errorf("the help does not list the host's command:\n%s", stdout)
	}

	// The name of the command is taken: a call cannot have it.
	h.overlay("resources:\n  pets:\n    setters:\n      - {name: oldest}\n")
	_, stderr, code = h.runBare("config")
	if code != apikit.ExitFailed || !strings.Contains(stderr, `"oldest" is the name of a built-in command`) {
		t.Errorf("a call named like a host command: exit code = %d, stderr = %q", code, stderr)
	}
}
