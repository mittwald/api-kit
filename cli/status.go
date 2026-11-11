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
type status struct {
	mu    sync.Mutex
	block *live.Live
	shown bool
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
	if s.shown {
		_ = s.block.Clear()
		s.shown = false
	}
}
