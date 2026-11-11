package argv_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mittwald/api-kit/cli/argv"
)

// tree builds a command line like the one a tool has:
//
//	tool [-v] [-o FORMAT] [--color[=WHEN]]
//	  get RESOURCE [ID] [-f FILTER]... [-A] [-L N]
//	  rename
//	    pets [ID] [--name NAME] [--tag TAG...] [--if-none RESOURCE CONDITION...]
//	  apply
//	    pets [ID] rename|name ...   (the ID may stand in front)
//	    owners rename ...           (no ID in front)
func tree() *cobra.Command {
	noop := func(*cobra.Command, []string) error { return nil }
	root := &cobra.Command{Use: "tool", RunE: noop}
	root.PersistentFlags().BoolP("verbose", "v", false, "")
	root.PersistentFlags().StringP("output", "o", "text", "")
	root.PersistentFlags().String("color", "auto", "")
	root.PersistentFlags().Lookup("color").NoOptDefVal = "always"
	annotate(root.PersistentFlags().Lookup("output"), argv.WordsPair+"jsonpath,columns")

	get := &cobra.Command{Use: "get", RunE: noop}
	get.Flags().StringArrayP("filter", "f", nil, "")
	get.Flags().BoolP("all", "A", false, "")
	get.Flags().IntP("limit", "L", 0, "")

	leaf := func(use string, aliases ...string) *cobra.Command {
		cmd := &cobra.Command{Use: use, Aliases: aliases, RunE: noop}
		cmd.Flags().String("name", "", "")
		cmd.Flags().StringArray("tag", nil, "")
		cmd.Flags().StringArray("if-none", nil, "")
		annotate(cmd.Flags().Lookup("tag"), argv.WordsRepeat)
		annotate(cmd.Flags().Lookup("if-none"), argv.WordsJoin)
		return cmd
	}
	rename := &cobra.Command{Use: "rename", RunE: noop}
	rename.AddCommand(leaf("pets"))

	apply := &cobra.Command{Use: "apply", RunE: noop}
	applyPets := &cobra.Command{Use: "pets", RunE: noop, Annotations: map[string]string{argv.AnnotationIDFirst: "true"}}
	applyPets.AddCommand(leaf("rename", "name"))
	applyOwners := &cobra.Command{Use: "owners", RunE: noop}
	applyOwners.AddCommand(leaf("rename"))
	apply.AddCommand(applyPets, applyOwners)

	root.AddCommand(get, rename, apply)
	return root
}

func annotate(flag *pflag.Flag, mode string) {
	flag.Annotations = map[string][]string{argv.AnnotationWords: {mode}}
}

// words splits a command line written with single spaces; "␣" stands for a
// space inside a word and "∅" for an empty word.
func words(line string) []string {
	if line == "" {
		return nil
	}
	parts := strings.Split(line, " ")
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "␣", " "), "∅", "")
	}
	return parts
}

// sep stands for the separator of joined words in expectations.
const sep = "|"

func TestRewrite(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string // "|" stands for argv.Separator
	}{
		{"nothing", "", ""},
		{"what cobra parses anyway", "get pets p-1 -f species=dog --all -L 5", "get pets p-1 -f species=dog --all -L 5"},
		{"values that look like options stay with their flag", "get pets -f -x -L -5", "get pets -f -x -L -5"},
		{"unknown options are left to cobra", "get --nope pets -Z", "get --nope pets -Z"},

		// A keyword and its argument as two words.
		{"jsonpath as two words", "get pets -o jsonpath $.name", "get pets --output=jsonpath=$.name"},
		{"columns as two words, long option", "get pets --output columns id,name", "get pets --output=columns=id,name"},
		{"in front of the command", "-o jsonpath $.name get pets", "--output=jsonpath=$.name get pets"},
		{"after flags that share the dash", "get pets -Avo jsonpath $.name", "get pets -Av --output=jsonpath=$.name"},
		{"one word already", "get pets -o jsonpath=$.name", "get pets -o jsonpath=$.name"},
		{"attached to the short option", "get pets -ojson p-1", "get pets -ojson p-1"},
		{"with an equals sign", "get pets --output=json p-1", "get pets --output=json p-1"},
		{"another format takes one word", "get pets -o json p-1", "get pets -o json p-1"},
		{"keyword at the end", "get pets -o jsonpath", "get pets -o jsonpath"},
		{"keyword before an option", "get pets -o jsonpath --all", "get pets -o jsonpath --all"},
		{"an expression that starts with a dash and holds a space", "get pets -o jsonpath -␣x", "get pets --output=jsonpath=-␣x"},

		// Several words for one flag.
		{"words joined into one value", "rename pets p-1 --if-none visits status=open note=a␣b --name Rex",
			"rename pets p-1 --if-none=visits|status=open|note=a␣b --name Rex"},
		{"words up to the end", "rename pets --if-none visits status=open", "rename pets --if-none=visits|status=open"},
		{"words up to a double dash", "rename pets --if-none visits -- status=open", "rename pets --if-none=visits -- status=open"},
		{"a negative number and a lone dash are words", "rename pets --if-none visits count=-1 -5 -", "rename pets --if-none=visits|count=-1|-5|-"},
		{"given twice", "rename pets --if-none a b --if-none c", "rename pets --if-none=a|b --if-none=c"},
		{"with an equals sign it is one word", "rename pets --if-none=visits status=open", "rename pets --if-none=visits status=open"},
		{"without a word", "rename pets --if-none --name Rex", "rename pets --if-none --name Rex"},
		{"each word to a flag of its own", "rename pets --tag calm old --name Rex", "rename pets --tag calm --tag old --name Rex"},
		{"one word", "rename pets --tag calm", "rename pets --tag calm"},
		{"several-word flags are not known above their command", "rename --tag a b pets", "rename --tag a b pets"},

		// An ID in front of a subcommand.
		{"ID before the call", "apply pets p-1 rename --name Rex", "apply pets rename p-1 --name Rex"},
		{"ID before the alias of a call", "apply pets p-1 name", "apply pets name p-1"},
		{"options between ID and call", "apply pets p-1 -v --color rename --name Rex", "apply pets -v --color rename p-1 --name Rex"},
		{"ID after the call", "apply pets rename p-1 --name Rex", "apply pets rename p-1 --name Rex"},
		{"no ID", "apply pets rename --name Rex", "apply pets rename --name Rex"},
		{"an ID that no call follows", "apply pets p-1", "apply pets p-1"},
		{"an ID that an unknown call follows", "apply pets p-1 nope --name Rex", "apply pets p-1 nope --name Rex"},
		{"an ID before a double dash", "apply pets p-1 -- rename", "apply pets p-1 -- rename"},
		{"a command that takes no ID in front", "apply owners o-1 rename", "apply owners o-1 rename"},
		{"an ID named like a call is the call", "apply pets rename rename", "apply pets rename rename"},

		// After a double dash nothing is touched.
		{"after a double dash", "get pets -- -o jsonpath $.name", "get pets -- -o jsonpath $.name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := argv.Rewrite(tree(), words(tt.args))
			want := words(strings.ReplaceAll(tt.want, sep, argv.Separator))
			if !slices.Equal(got.Args, want) {
				t.Errorf("Rewrite(%q)\n got %q\nwant %q", tt.args, got.Args, want)
			}
			if got.Pending != nil {
				t.Errorf("Pending = %+v outside a completion request", got.Pending)
			}
		})
	}
}

func TestRewriteSelectsTheCommandCobraFinds(t *testing.T) {
	lines := []string{
		"get pets p-1", "get pets -o jsonpath $.name", "-o columns id get pets",
		"rename pets p-1 --if-none visits status=open --name Rex", "rename pets --tag a b",
		"apply pets p-1 rename --name Rex", "apply pets p-1 -v name", "apply owners rename o-1",
		"apply pets p-1", "apply",
	}
	wants := []string{
		"tool get", "tool get", "tool get",
		"tool rename pets", "tool rename pets",
		"tool apply pets rename", "tool apply pets rename", "tool apply owners rename",
		"tool apply pets", "tool apply",
	}
	for i, line := range lines {
		root := tree()
		rewritten := argv.Rewrite(root, words(line)).Args
		command, _, err := root.Find(rewritten)
		if err != nil {
			t.Errorf("%q: Find: %v", line, err)
			continue
		}
		if got := command.CommandPath(); got != wants[i] {
			t.Errorf("%q runs %q, want %q", line, got, wants[i])
		}
		// What was rewritten must parse.
		if err := command.ParseFlags(rewritten); err != nil {
			t.Errorf("%q: the rewritten line %q does not parse: %v", line, rewritten, err)
		}
	}
}

func TestRewriteReportsSpellingsWithAReplacement(t *testing.T) {
	got := argv.Rewrite(tree(), words("apply pets p-1 rename -o jsonpath $.name --if-none a b --tag x y"))
	want := []argv.Notice{
		{Kind: argv.IDFirst, Was: []string{"p-1", "rename"}, Now: []string{"rename", "p-1"}},
		{Kind: argv.PairedValue, Was: []string{"-o", "jsonpath", "$.name"}, Now: []string{"-o", "jsonpath=$.name"}},
	}
	if len(got.Notices) != len(want) {
		t.Fatalf("Notices = %+v", got.Notices)
	}
	for i, notice := range got.Notices {
		if notice.Kind != want[i].Kind || !slices.Equal(notice.Was, want[i].Was) || !slices.Equal(notice.Now, want[i].Now) {
			t.Errorf("notice %d = %+v, want %+v", i, notice, want[i])
		}
	}
	if clean := argv.Rewrite(tree(), words("get pets -o json")); len(clean.Notices) != 0 {
		t.Errorf("a line that needs no rewriting has notices: %+v", clean.Notices)
	}
}

func TestRewriteCompletionRequest(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		want    string
		pending *argv.Pending
	}{
		{"a plain request", "__complete get pe", "__complete get pe", nil},
		{"an empty word to complete", "__complete get pets ∅", "__complete get pets ∅", nil},
		{"the word to complete is not rewritten", "__complete get pets -o jsonpath", "__complete get pets -o jsonpath", nil},
		{"what stands before it is", "__complete get pets -o jsonpath $.name ∅", "__complete get pets --output=jsonpath=$.name ∅", nil},
		{"an ID before the call", "__complete apply pets p-1 rename --na", "__complete apply pets rename p-1 --na", nil},
		{"an ID before the call that is being typed", "__complete apply pets p-1 ren", "__complete apply pets ren", nil},
		{"an ID before a call still to come", "__complete apply pets p-1 ∅", "__complete apply pets ∅", nil},
		{"the word continues a several-word value", "__complete rename pets --if-none visits sta",
			"__complete rename pets --if-none=visits sta", &argv.Pending{Flag: "if-none", Words: []string{"visits"}}},
		{"the first word of the value is completed by the flag", "__complete rename pets --if-none vis",
			"__complete rename pets --if-none vis", nil},
		{"a value that has ended", "__complete rename pets --if-none visits a=b --name ∅",
			"__complete rename pets --if-none=visits|a=b --name ∅", nil},
		{"without descriptions", "__completeNoDesc rename pets --tag a b", "__completeNoDesc rename pets --tag a b",
			&argv.Pending{Flag: "tag", Words: []string{"a"}}},
		{"only the request", "__complete ∅", "__complete ∅", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := argv.Rewrite(tree(), words(tt.args))
			want := words(strings.ReplaceAll(tt.want, sep, argv.Separator))
			if !slices.Equal(got.Args, want) {
				t.Errorf("Rewrite(%q)\n got %q\nwant %q", tt.args, got.Args, want)
			}
			switch {
			case tt.pending == nil && got.Pending != nil:
				t.Errorf("Pending = %+v, want none", got.Pending)
			case tt.pending != nil && (got.Pending == nil || got.Pending.Flag != tt.pending.Flag || !slices.Equal(got.Pending.Words, tt.pending.Words)):
				t.Errorf("Pending = %+v, want %+v", got.Pending, tt.pending)
			}
			if len(got.Notices) != 0 {
				t.Errorf("a completion request has notices: %+v", got.Notices)
			}
		})
	}
}
