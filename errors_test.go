package apikit_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	apikit "github.com/mittwald/api-kit"
)

func TestKindOfAndExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind apikit.Kind
		code int
	}{
		{"nil", nil, 0, apikit.ExitOK},
		{"plain", errors.New("boom"), apikit.KindFailed, apikit.ExitFailed},
		{"failed", apikit.Errorf(apikit.KindFailed, "boom"), apikit.KindFailed, apikit.ExitFailed},
		{"usage", apikit.Errorf(apikit.KindUsage, "bad flag"), apikit.KindUsage, apikit.ExitUsage},
		{"skipped", apikit.Errorf(apikit.KindSkipped, "not needed"), apikit.KindSkipped, apikit.ExitSkipped},
		{"not found", apikit.Errorf(apikit.KindNotFound, "no such thing"), apikit.KindNotFound, apikit.ExitNotFound},
		{"unauthenticated", apikit.Errorf(apikit.KindUnauthenticated, "no token"), apikit.KindUnauthenticated, apikit.ExitFailed},
		{"definition", apikit.Errorf(apikit.KindDefinition, "bad key"), apikit.KindDefinition, apikit.ExitFailed},
		{"canceled context", context.Canceled, apikit.KindCanceled, apikit.ExitCanceled},
		{"wrapped canceled context", fmt.Errorf("fetch: %w", context.Canceled), apikit.KindCanceled, apikit.ExitCanceled},
		{"wrapped error", fmt.Errorf("outer: %w", apikit.Errorf(apikit.KindNotFound, "gone")), apikit.KindNotFound, apikit.ExitNotFound},
		{"kind wins over cause", apikit.Errorf(apikit.KindUsage, "stopped: %w", context.Canceled), apikit.KindUsage, apikit.ExitUsage},
		{"zero kind", &apikit.Error{Msg: "unclassified"}, apikit.KindFailed, apikit.ExitFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apikit.KindOf(tt.err); got != tt.kind {
				t.Errorf("KindOf = %v, want %v", got, tt.kind)
			}
			if got := apikit.ExitCode(tt.err); got != tt.code {
				t.Errorf("ExitCode = %d, want %d", got, tt.code)
			}
		})
	}
}

func TestErrorfKeepsMessageAndCause(t *testing.T) {
	err := apikit.Errorf(apikit.KindFailed, "read %s: %w", "definition.yaml", fs.ErrNotExist)
	if got, want := err.Error(), "read definition.yaml: file does not exist"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("the wrapped cause is not reachable with errors.Is")
	}
	if cause := apikit.Errorf(apikit.KindFailed, "no cause").Unwrap(); cause != nil {
		t.Errorf("Unwrap() = %v, want nil", cause)
	}
}

func TestKindString(t *testing.T) {
	names := map[apikit.Kind]string{
		0:                          "none",
		apikit.KindFailed:          "failed",
		apikit.KindUsage:           "usage",
		apikit.KindSkipped:         "skipped",
		apikit.KindNotFound:        "not found",
		apikit.KindUnauthenticated: "unauthenticated",
		apikit.KindDefinition:      "definition",
		apikit.KindCanceled:        "canceled",
	}
	for kind, want := range names {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}
