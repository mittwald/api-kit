// Package argv rewrites a command line into the form cobra parses, so that
// spellings cobra does not know keep working:
//
//   - An option whose value is several words, as in "--if-none visits
//     status=open", where cobra takes one word as the value.
//   - An option whose value is a keyword and its argument as two words, as
//     in "-o jsonpath $.name" for "-o jsonpath=$.name".
//   - An ID in front of a subcommand, as in "apply pets p-1 rename", where
//     cobra wants the subcommand first.
//
// [Rewrite] walks the words from left to right along the command tree, the
// way cobra finds the command to run, and looks at annotations on flags and
// commands to know which rewriting applies where. It knows nothing about
// the commands themselves.
package argv

import (
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// AnnotationWords is the annotation of a flag whose value may be written
// as several words. Its value is one of:
//
//   - "repeat": each word is one value of a flag that can be given several
//     times; "--tag a b" becomes "--tag a --tag b".
//   - "join": the words form one value, joined with [Separator].
//   - "pair:" and a comma-separated list of keywords: when the value is one
//     of the keywords and another word follows, the two become one value
//     joined with "="; "-o jsonpath $.name" becomes "--output=jsonpath=$.name".
const AnnotationWords = "apikit_words"

// The modes of [AnnotationWords].
const (
	// WordsRepeat gives each word to the flag on its own.
	WordsRepeat = "repeat"
	// WordsJoin gives all words to the flag as one value.
	WordsJoin = "join"
	// WordsPair starts the mode that joins a keyword and its argument.
	WordsPair = "pair:"
)

// AnnotationIDFirst is the annotation of a command that accepts an ID in
// front of its subcommand. Any value switches it on.
const AnnotationIDFirst = "apikit_id_first"

// Separator joins the words of a [WordsJoin] value. It is the ASCII unit
// separator, which no word on a command line contains.
const Separator = "\x1f"

// Kind says which spelling a [Notice] is about.
type Kind uint8

// The spellings Rewrite reports.
const (
	// PairedValue is a keyword and its argument given as two words.
	PairedValue Kind = iota + 1
	// IDFirst is an ID in front of a subcommand.
	IDFirst
)

// Notice reports a spelling that was rewritten, for a caller that wants to
// tell the user about the spelling to prefer.
type Notice struct {
	// Kind says which spelling it is.
	Kind Kind
	// Was is what was written, as words.
	Was []string
	// Now is what it was rewritten to, as words.
	Now []string
}

// Pending describes a several-word value that is still being typed when
// the command line is a completion request: the flag and the words given
// so far, not counting the word being completed.
type Pending struct {
	// Flag is the name of the flag.
	Flag string
	// Words are the complete words of the value.
	Words []string
}

// Result is a rewritten command line.
type Result struct {
	// Args is the command line for cobra.
	Args []string
	// Notices list what was rewritten from a spelling with a replacement.
	Notices []Notice
	// Pending is set for a completion request whose last word continues a
	// several-word value.
	Pending *Pending
}

// Rewrite returns args, the command line without the program name, in the
// form cobra parses. Words after "--" are left alone. A completion request
// (cobra's "__complete" command) is rewritten up to the word being
// completed, which stays as it is.
func Rewrite(root *cobra.Command, args []string) Result {
	r := rewriter{cmd: root}
	if len(args) >= 2 && (args[0] == cobra.ShellCompRequestCmd || args[0] == cobra.ShellCompNoDescRequestCmd) {
		r.completing = true
		r.walk(args[1 : len(args)-1])
		last := args[len(args)-1]
		return Result{
			Args:    slices.Concat([]string{args[0]}, r.out, []string{last}),
			Pending: r.pending,
		}
	}
	r.walk(args)
	return Result{Args: r.out, Notices: r.notices}
}

type rewriter struct {
	cmd        *cobra.Command
	out        []string
	notices    []Notice
	completing bool
	pending    *Pending
	// held is an ID seen in front of a subcommand; heldAt is where it goes
	// back when no subcommand follows.
	held   *string
	heldAt int
}

func (r *rewriter) walk(args []string) {
	for i := 0; i < len(args); {
		arg := args[i]
		switch {
		case arg == "--":
			r.release()
			r.out = append(r.out, args[i:]...)
			return
		case strings.HasPrefix(arg, "--"):
			i += r.long(arg, args[i+1:])
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			i += r.short(arg, args[i+1:])
		default:
			r.positional(arg)
			i++
		}
	}
	if r.completing {
		// The word being completed follows; after an ID in front of a
		// subcommand it can only be the subcommand, which cobra offers
		// when the ID is out of its way.
		r.held = nil
		return
	}
	r.release()
}

// long handles "--name" or "--name=value" and returns how many words it
// used up.
func (r *rewriter) long(arg string, rest []string) int {
	name, _, hasValue := strings.Cut(arg[2:], "=")
	flag := r.flag(name)
	if flag == nil || hasValue || flag.NoOptDefVal != "" {
		r.out = append(r.out, arg)
		return 1
	}
	return 1 + r.value(flag, []string{arg}, rest)
}

// short handles "-x", a cluster such as "-abc", and "-xvalue", and returns
// how many words it used up.
func (r *rewriter) short(arg string, rest []string) int {
	letters := arg[1:]
	for j := range len(letters) {
		flag := r.shorthand(letters[j : j+1])
		if flag == nil {
			break
		}
		if flag.NoOptDefVal != "" {
			continue
		}
		if j < len(letters)-1 {
			// The value is attached: -ojson.
			break
		}
		written := []string{arg}
		if j > 0 {
			// Flags without a value stand in front: -Ao is -A -o.
			written = []string{"-" + letters[:j], "-" + letters[j:]}
		}
		return 1 + r.value(flag, written, rest)
	}
	r.out = append(r.out, arg)
	return 1
}

// value emits a flag that takes a value from the following words and
// returns how many of them it used up. written is the flag as it was
// written; its last word is the flag itself, any before it are flags
// without a value that shared its dash.
func (r *rewriter) value(flag *pflag.Flag, written, rest []string) int {
	before := written[:len(written)-1]
	mode := flag.Annotations[AnnotationWords]
	switch {
	case len(mode) == 0:
	case mode[0] == WordsRepeat:
		words := leadingWords(rest)
		if len(words) == 0 {
			break
		}
		r.out = append(r.out, before...)
		for _, word := range words {
			r.out = append(r.out, "--"+flag.Name, word)
		}
		r.wait(flag, words, len(rest))
		return len(words)
	case mode[0] == WordsJoin:
		words := leadingWords(rest)
		if len(words) == 0 {
			break
		}
		r.out = append(r.out, before...)
		r.out = append(r.out, "--"+flag.Name+"="+strings.Join(words, Separator))
		r.wait(flag, words, len(rest))
		return len(words)
	case strings.HasPrefix(mode[0], WordsPair):
		heads := strings.Split(strings.TrimPrefix(mode[0], WordsPair), ",")
		if len(rest) >= 2 && slices.Contains(heads, rest[0]) && isWord(rest[1]) {
			r.out = append(r.out, before...)
			r.out = append(r.out, "--"+flag.Name+"="+rest[0]+"="+rest[1])
			spelled := written[len(written)-1]
			r.notices = append(r.notices, Notice{
				Kind: PairedValue,
				Was:  []string{spelled, rest[0], rest[1]},
				Now:  []string{spelled, rest[0] + "=" + rest[1]},
			})
			return 2
		}
	}
	r.out = append(r.out, written...)
	if len(rest) > 0 {
		r.out = append(r.out, rest[0])
		return 1
	}
	return 0
}

// wait records a several-word value that reaches the end of a completion
// request: the word being completed belongs to it.
func (r *rewriter) wait(flag *pflag.Flag, words []string, available int) {
	if r.completing && len(words) == available {
		r.pending = &Pending{Flag: flag.Name, Words: slices.Clone(words)}
	}
}

// positional handles a word that is no option: a subcommand, or an
// argument.
func (r *rewriter) positional(arg string) {
	if child := r.child(arg); child != nil {
		r.out = append(r.out, arg)
		if r.held != nil {
			// The ID stood in front of the subcommand; cobra wants it after.
			id := *r.held
			r.notices = append(r.notices, Notice{Kind: IDFirst, Was: []string{id, arg}, Now: []string{arg, id}})
			r.out = append(r.out, id)
			r.held = nil
		}
		r.cmd = child
		return
	}
	if _, ok := r.cmd.Annotations[AnnotationIDFirst]; ok && r.held == nil && r.cmd.HasSubCommands() {
		r.held, r.heldAt = &arg, len(r.out)
		return
	}
	r.release()
	r.out = append(r.out, arg)
}

// release puts a held ID back where it stood, when no subcommand followed
// it.
func (r *rewriter) release() {
	if r.held == nil {
		return
	}
	r.out = slices.Insert(r.out, r.heldAt, *r.held)
	r.held = nil
}

// child returns the subcommand of the current command with that name or
// alias.
func (r *rewriter) child(name string) *cobra.Command {
	for _, command := range r.cmd.Commands() {
		if command.Name() == name || command.HasAlias(name) {
			return command
		}
	}
	return nil
}

// flag returns the flag with that name that the current command accepts:
// one of its own, or a persistent one of a command above it.
func (r *rewriter) flag(name string) *pflag.Flag {
	return r.find(func(flags *pflag.FlagSet) *pflag.Flag { return flags.Lookup(name) })
}

// shorthand returns the flag with that one-letter name.
func (r *rewriter) shorthand(letter string) *pflag.Flag {
	return r.find(func(flags *pflag.FlagSet) *pflag.Flag { return flags.ShorthandLookup(letter) })
}

func (r *rewriter) find(lookup func(*pflag.FlagSet) *pflag.Flag) *pflag.Flag {
	if flag := lookup(r.cmd.Flags()); flag != nil {
		return flag
	}
	for command := r.cmd; command != nil; command = command.Parent() {
		if flag := lookup(command.PersistentFlags()); flag != nil {
			return flag
		}
	}
	return nil
}

// leadingWords returns the words at the start of args that belong to a
// several-word value: everything up to "--" or the next option.
func leadingWords(args []string) []string {
	for i, arg := range args {
		if !isWord(arg) {
			return args[:i]
		}
	}
	return args
}

// isWord reports whether a word of the command line is a value and no
// option. A word that starts with a dash is an option, unless it is a
// negative number, a lone dash, or holds white space, which no option does.
func isWord(arg string) bool {
	if !strings.HasPrefix(arg, "-") || arg == "-" {
		return true
	}
	if arg == "--" {
		return false
	}
	if strings.ContainsAny(arg, " \t\n") {
		return true
	}
	_, err := strconv.ParseFloat(arg, 64)
	return err == nil
}
