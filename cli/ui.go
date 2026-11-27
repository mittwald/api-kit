package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/mittwald/cli-kit/browser"
	"github.com/mittwald/cli-kit/pick"
	"github.com/mittwald/cli-kit/term"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// terminal lets package auth talk to the person at the terminal: ask for
// credentials, open the browser, and say what is going on. Questions and
// notes go to the error stream, so the output stream keeps the result alone.
type terminal struct {
	in  io.Reader
	out io.Writer
	err io.Writer
	// lines reads the answers of a login from a piped input, one line
	// each.
	lines *pipedLines
}

// pipedLines reads lines from an input that is no terminal, one answer
// after the other. It reads a byte at a time, so that what follows the
// answers is left for whoever reads the input next.
type pipedLines struct{}

// next returns the next line of in, without its line break. The last line
// may lack one.
func (p *pipedLines) next(in io.Reader) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := in.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			line = append(line, b[0])
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return strings.TrimRight(string(line), "\r"), nil
}

// files returns the streams to ask a question on, when both the input and
// the error stream are terminals.
func (t terminal) files() (in, out *os.File, ok bool) {
	in, _ = t.in.(*os.File)
	out, _ = t.err.(*os.File)
	return in, out, term.Interactive(in, out)
}

// Interactive reports whether someone is there to answer: the input, the
// output and the error stream are all terminals. A run whose output is
// piped does not start to ask questions.
func (t terminal) Interactive() bool {
	_, _, ok := t.files()
	out, _ := t.out.(*os.File)
	return ok && out != nil && term.IsTerminal(out)
}

// Ask shows the hint of a prompt, asks its question, and returns the
// answer. What is typed for a secret is not shown.
func (t terminal) Ask(_ context.Context, p definition.Prompt) (string, error) {
	in, out, ok := t.files()
	if !ok && t.piped() {
		// A login run in a script gets its answers from standard input, one
		// line each, in the order of the prompts.
		answer, err := t.lines.next(t.in)
		if err != nil {
			return "", apikit.Errorf(apikit.KindUnauthenticated, "no line for %s on standard input: %w", p.Label, err)
		}
		return answer, nil
	}
	if !ok {
		return "", apikit.Errorf(apikit.KindUnauthenticated, "cannot ask for %s without a terminal", p.Label)
	}
	if p.Hint != "" {
		writeNotice(t.err, p.Hint)
	}
	answer, err := pick.Input(in, out, pick.InputOptions{Prompt: p.Label + ": ", Secret: p.Secret})
	switch {
	case errors.Is(err, pick.ErrInterrupted), errors.Is(err, pick.ErrBack), errors.Is(err, pick.ErrAborted):
		return "", context.Canceled
	case err != nil:
		return "", err
	}
	return answer, nil
}

// piped reports whether the input is a pipe or a file to read answers
// from, rather than a terminal or nothing at all.
func (t terminal) piped() bool {
	if t.lines == nil || t.in == nil {
		return false
	}
	file, ok := t.in.(*os.File)
	return !ok || !term.IsTerminal(file)
}

// OpenBrowser opens a page in the browser of the desktop. It returns at
// once; the caller tells the user the address in case nothing opens. Only
// an http or https address is opened (browser.Open of cli-kit), so an
// address the API had a say in cannot start another program.
func (t terminal) OpenBrowser(ctx context.Context, url string) error {
	return browser.Open(ctx, url)
}

// Notify tells the user something on the error stream.
func (t terminal) Notify(text string) {
	writeNotice(t.err, text)
}
