package expr_test

import (
	"testing"

	apikit "github.com/mittwald/api-kit"
)

// decode parses JSON for a test and fails the test when it is malformed.
func decode(t *testing.T, text string) any {
	t.Helper()
	value, err := apikit.Unmarshal([]byte(text))
	if err != nil {
		t.Fatalf("Unmarshal(%s): %v", text, err)
	}
	return value
}

// lookupIn returns a lookup function that knows the keys of values.
func lookupIn(values map[string]any) func(string) (any, bool) {
	return func(name string) (any, bool) {
		value, ok := values[name]
		return value, ok
	}
}
