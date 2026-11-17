package cli

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mittwald/cli-kit/live"

	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// screen is a terminal as far as a status line needs one: it keeps rows of
// text and understands a carriage return, a newline, moving the cursor up
// (ESC [ n A) and erasing below the cursor (ESC [ J). The output and the
// error stream of a tool both write to it, as they do to one terminal.
type screen struct {
	mu     sync.Mutex
	rows   []string
	row    int
	column int
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := string(p)
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '\r':
			s.column = 0
		case c == '\n':
			s.row++
			s.column = 0
		case c == '\x1b' && i+1 < len(text) && text[i+1] == '[':
			end := i + 2
			for end < len(text) && text[end] >= '0' && text[end] <= '9' {
				end++
			}
			n, _ := strconv.Atoi(text[i+2 : end])
			switch text[end] {
			case 'A':
				s.row = max(0, s.row-max(n, 1))
			case 'J':
				s.grow()
				s.rows[s.row] = s.rows[s.row][:min(s.column, len(s.rows[s.row]))]
				s.rows = s.rows[:s.row+1]
			}
			i = end
		default:
			s.grow()
			line := s.rows[s.row]
			if len(line) < s.column {
				line += strings.Repeat(" ", s.column-len(line))
			}
			s.rows[s.row] = line[:s.column] + string(c) + line[min(len(line), s.column+1):]
			s.column++
		}
	}
	return len(p), nil
}

// grow adds rows up to the cursor's.
func (s *screen) grow() {
	for len(s.rows) <= s.row {
		s.rows = append(s.rows, "")
	}
}

// shown returns the rows that hold text.
func (s *screen) shown() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []string
	for _, row := range s.rows {
		if row != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

// withStatus returns a tool that writes both streams to a screen and has
// a status line on it, as a run on a terminal has once its session is
// made.
func withStatus(t *testing.T) (*App, *screen) {
	t.Helper()
	scr := &screen{}
	a := New(Options{Name: "example", Streams: Streams{In: strings.NewReader(""), Out: scr, Err: scr}})
	if _, err := a.Build(nil); err != nil {
		t.Fatal(err)
	}
	a.status = &status{block: live.New(scr)}
	return a, scr
}

func TestStatusLineMakesWayForTheErrorStream(t *testing.T) {
	a, scr := withStatus(t)
	a.status.report(engine.Progress{Stage: engine.StageFetch})
	// A warning while the line is shown, as a deprecated spelling causes.
	a.deprecated("--old", "--new")
	a.status.report(engine.Progress{Stage: engine.StageFetch, Done: 5})
	writeLine(a.errs, "ERROR: the API answered 500")
	a.status.report(engine.Progress{Stage: engine.StageFetch, Done: 10})
	_, _ = a.out.Write([]byte("result\n"))
	a.status.clear()

	want := []string{"WARNING: --old is deprecated, use --new", "ERROR: the API answered 500", "result"}
	if got := scr.shown(); !slices.Equal(got, want) {
		t.Errorf("the screen shows %q, want %q", got, want)
	}
}

// promptUI asks on the screen and has the status reported while the
// question is open, as a fetch that runs alongside would.
type promptUI struct {
	scr    *screen
	during func()
}

func (promptUI) Interactive() bool                         { return true }
func (promptUI) OpenBrowser(context.Context, string) error { return nil }
func (u promptUI) Notify(text string)                      { writeLine(u.scr, text) }
func (u promptUI) Ask(context.Context, definition.Prompt) (string, error) {
	_, _ = u.scr.Write([]byte("Password: "))
	u.during()
	_, _ = u.scr.Write([]byte("\n"))
	return "hunter2", nil
}

func TestStatusLineStaysOffDuringAQuestion(t *testing.T) {
	a, scr := withStatus(t)
	ui := heldUI{status: a.status, UI: promptUI{scr: scr, during: func() {
		a.status.report(engine.Progress{Stage: engine.StageFetch, Done: 3})
	}}}
	a.status.report(engine.Progress{Stage: engine.StageFetch})
	ui.Notify("Opening the browser")
	a.status.report(engine.Progress{Stage: engine.StageFetch, Done: 1})
	if _, err := ui.Ask(context.Background(), definition.Prompt{Label: "Password", Secret: true}); err != nil {
		t.Fatal(err)
	}
	a.status.report(engine.Progress{Stage: engine.StageFetch, Done: 4})
	a.status.clear()

	want := []string{"Opening the browser", "Password: "}
	if got := scr.shown(); !slices.Equal(got, want) {
		t.Errorf("the screen shows %q, want %q", got, want)
	}
}

func TestStatusLineAndWritesFromSeveralGoroutines(t *testing.T) {
	a, scr := withStatus(t)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 50 {
				a.status.report(engine.Progress{Stage: engine.StageFetch, Done: i*100 + j})
			}
		})
	}
	for i := range 20 {
		writeLine(a.errs, "line "+strconv.Itoa(i))
	}
	wg.Wait()
	a.status.clear()
	got := scr.shown()
	if len(got) != 20 || got[0] != "line 0" || got[19] != "line 19" {
		t.Errorf("the screen shows %q", got)
	}
}
