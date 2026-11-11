package apikit

import (
	"context"
	"errors"
	"fmt"
)

// Kind classifies an error by what a command-line tool does about it.
type Kind uint8

// The kinds of errors. The zero Kind means "no error".
const (
	// KindFailed is any failure that has no more specific kind: a request
	// the API refused, an unreadable file, an unexpected response.
	KindFailed Kind = iota + 1
	// KindUsage is a mistake on the command line: an unknown option, a
	// missing argument, values that may not be combined.
	KindUsage
	// KindSkipped means nothing was done on purpose: a condition the caller
	// set does not hold, or a result that had to be non-empty is empty.
	KindSkipped
	// KindNotFound means the resource asked for does not exist.
	KindNotFound
	// KindUnauthenticated means no usable credential is available.
	KindUnauthenticated
	// KindDefinition is a mistake in a definition document.
	KindDefinition
	// KindCanceled means the run was interrupted.
	KindCanceled
)

// Exit codes a command-line tool ends with.
const (
	// ExitOK is the exit code of a successful run.
	ExitOK = 0
	// ExitFailed is the exit code of a failed run.
	ExitFailed = 1
	// ExitUsage is the exit code for a mistake on the command line.
	ExitUsage = 2
	// ExitSkipped is the exit code when nothing was done on purpose.
	ExitSkipped = 3
	// ExitNotFound is the exit code when the resource does not exist.
	ExitNotFound = 4
	// ExitCanceled is the exit code of an interrupted run, 128 plus the
	// number of the interrupt signal.
	ExitCanceled = 130
)

// String returns the kind's name.
func (k Kind) String() string {
	switch k {
	case KindFailed:
		return "failed"
	case KindUsage:
		return "usage"
	case KindSkipped:
		return "skipped"
	case KindNotFound:
		return "not found"
	case KindUnauthenticated:
		return "unauthenticated"
	case KindDefinition:
		return "definition"
	case KindCanceled:
		return "canceled"
	}
	return "none"
}

// ExitCode returns the exit code a command-line tool ends with for an error
// of this kind. The zero Kind maps to [ExitOK].
func (k Kind) ExitCode() int {
	switch k {
	case 0:
		return ExitOK
	case KindUsage:
		return ExitUsage
	case KindSkipped:
		return ExitSkipped
	case KindNotFound:
		return ExitNotFound
	case KindCanceled:
		return ExitCanceled
	}
	return ExitFailed
}

// Error is an error with a [Kind]. Its message is complete as it stands: it
// carries no "error:" prefix and does not name the program.
type Error struct {
	// Kind classifies the error.
	Kind Kind
	// Msg is the message shown to the user.
	Msg string
	// Err is the underlying error, or nil.
	Err error
}

// Errorf returns an [*Error] of the given kind. An error among args that is
// wrapped with %w becomes the underlying error.
func Errorf(kind Kind, format string, args ...any) *Error {
	err := fmt.Errorf(format, args...)
	return &Error{Kind: kind, Msg: err.Error(), Err: errors.Unwrap(err)}
}

// Error returns the message.
func (e *Error) Error() string { return e.Msg }

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the kind of err: 0 for nil, the kind of the first [*Error]
// in its chain, [KindCanceled] for a canceled context, and [KindFailed] for
// anything else.
func KindOf(err error) Kind {
	if err == nil {
		return 0
	}
	if e, ok := errors.AsType[*Error](err); ok && e.Kind != 0 {
		return e.Kind
	}
	if errors.Is(err, context.Canceled) {
		return KindCanceled
	}
	return KindFailed
}

// ExitCode returns the exit code for err, [ExitOK] for nil.
func ExitCode(err error) int { return KindOf(err).ExitCode() }
