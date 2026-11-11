package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mittwald/cli-kit/buildinfo"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/cli"
)

// run executes a tool named "example" with args and returns what it wrote
// and its exit code.
func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
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
	stdout, stderr, code := run(t)
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
	stdout, stderr, code := run(t, "--help")
	if code != apikit.ExitOK {
		t.Errorf("exit code = %d, want %d", code, apikit.ExitOK)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Errorf("stdout is not the help text:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want it empty", stderr)
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		stdout, _, code := run(t, args...)
		if code != apikit.ExitOK {
			t.Errorf("%v: exit code = %d, want %d", args, code, apikit.ExitOK)
		}
		if stdout != "example 1.2.3\n" {
			t.Errorf("%v: stdout = %q, want %q", args, stdout, "example 1.2.3\n")
		}
	}
	stdout, _, code := run(t, "version")
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
			stdout, stderr, code := run(t, tt.args...)
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
			stdout, stderr, code := run(t, "completion", shell)
			if code != apikit.ExitOK {
				t.Fatalf("exit code = %d, stderr:\n%s", code, stderr)
			}
			if !strings.Contains(stdout, marker) {
				t.Errorf("the %s script does not contain %q", shell, marker)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want it empty", stderr)
			}
		})
	}
}

func TestCompletionRequestOffersCommands(t *testing.T) {
	stdout, _, code := run(t, "__complete", "")
	if code != apikit.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"version", "completion", "help"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("completion does not offer %q:\n%s", want, stdout)
		}
	}
}

func TestCanceledRunIsSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	app := cli.New(cli.Options{Name: "example", Streams: cli.Streams{Out: &out, Err: &errOut}})
	if code := app.Execute(ctx, []string{"version"}); code != apikit.ExitOK {
		// The version command does not look at the context; a canceled
		// context alone must not turn a finished command into a failure.
		t.Errorf("exit code = %d, want %d", code, apikit.ExitOK)
	}
}

func TestDefaultNameComesFromTheProgram(t *testing.T) {
	var out bytes.Buffer
	app := cli.New(cli.Options{Streams: cli.Streams{Out: &out, Err: &out}})
	if name := app.Build().Name(); name == "" {
		t.Error("the root command has no name")
	}
}
