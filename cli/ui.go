package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"

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
	if !ok {
		return "", apikit.Errorf(apikit.KindUnauthenticated, "cannot ask for %s without a terminal", p.Label)
	}
	if p.Hint != "" {
		writeLine(t.err, p.Hint)
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

// OpenBrowser opens a page in the browser of the desktop. It returns at
// once; the caller tells the user the address in case nothing opens.
func (t terminal) OpenBrowser(ctx context.Context, url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", url)
	case "windows":
		command = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.CommandContext(ctx, "xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return err
	}
	// Reap the launcher when it is done; its outcome says nothing about
	// whether a page opened.
	go func() { _ = command.Wait() }()
	return nil
}

// Notify tells the user something on the error stream.
func (t terminal) Notify(text string) {
	writeLine(t.err, text)
}
