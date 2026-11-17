package cli

import (
	"io"
	"strconv"
	"sync"

	"github.com/mittwald/cli-kit/live"
	"github.com/mittwald/cli-kit/term"

	"github.com/mittwald/api-kit/engine"
)

// status shows on a terminal what a long run is busy with, on one line
// that is redrawn in place and gone when the run is done. It is safe for
// concurrent use: fetches that run at the same time report at the same
// time.
//
// The line is found again by counting rows up from the cursor, so nothing
// else may reach the terminal while it is shown: a line written below it
// would be drawn over by the next report, and the status line itself left
// behind. Whatever the tool writes, to the output stream or to the error
// stream, therefore goes through [status.hold], and a question to the user
// through [status.pause].
type status struct {
	mu    sync.Mutex
	block *live.Live
	shown bool
	// paused counts the questions under way; no line is drawn meanwhile.
	paused int
}

// newStatus returns a status line for w, or nil when w is no terminal that
// can redraw a line.
func newStatus(w io.Writer) *status {
	if !term.CanAnimate(w) {
		return nil
	}
	return &status{block: live.New(w)}
}

// report shows how far the engine has come. It is the progress callback of
// the engine; a nil status ignores the report.
func (s *status) report(p engine.Progress) {
	if s == nil {
		return
	}
	var text string
	switch p.Stage {
	case engine.StageFetch:
		text = "Fetching data..."
		if p.Done > 0 {
			text = "Fetching data (" + strconv.Itoa(p.Done) + ")..."
		}
	case engine.StageJoin:
		text = "Joining data (" + strconv.Itoa(p.Done) + "/" + strconv.Itoa(p.Total) + ")..."
	default:
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused > 0 {
		return
	}
	// A line that cannot be drawn is not worth failing a run for.
	if err := s.block.Draw([]string{text}); err == nil {
		s.shown = true
	}
}

// clear removes the line, before anything else is written to the
// terminal.
func (s *status) clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked()
}

// clearLocked removes the line; s.mu is held.
func (s *status) clearLocked() {
	if s.shown {
		_ = s.block.Clear()
		s.shown = false
	}
}

// hold calls write, which writes to the terminal, with the line taken off
// it, and keeps the line from being drawn again until write is done, so a
// report from another goroutine cannot come between the two. write must
// not write through hold itself. A nil status just calls write.
func (s *status) hold(write func()) {
	if s == nil {
		write()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked()
	write()
}

// pause takes the line off the terminal and keeps it off until the
// returned function is called: while the user is asked something, a line
// drawn under the question would land in the middle of the answer.
// Writes through [status.hold] stay possible meanwhile. A nil status does
// nothing.
func (s *status) pause() (resume func()) {
	if s == nil {
		return func() {}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearLocked()
	s.paused++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.paused--
		})
	}
}
