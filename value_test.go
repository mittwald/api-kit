package apikit_test

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
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

func TestDecodeKeepsOrderAndNumbers(t *testing.T) {
	const source = `{"zeta":1,"alpha":{"b":[1.0,12345678901234567890,1e2],"a":null},"mid":"<a&b>","flag":true}`
	value := decode(t, source)

	object, ok := value.(*apikit.Object)
	if !ok {
		t.Fatalf("decoded %T, want *apikit.Object", value)
	}
	if got, want := object.Keys(), []string{"zeta", "alpha", "mid", "flag"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}

	encoded, err := apikit.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != source {
		t.Errorf("round trip changed the document:\n got %s\nwant %s", encoded, source)
	}
}

func TestDecodeDuplicateKeyKeepsFirstPositionAndLastValue(t *testing.T) {
	object := decode(t, `{"a":1,"b":2,"a":3}`).(*apikit.Object)
	if got, want := object.Keys(), []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
	if value, _ := object.Get("a"); value != json.Number("3") {
		t.Errorf(`Get("a") = %v, want 3`, value)
	}
}

func TestDecodeEmptyListIsNotNil(t *testing.T) {
	list, ok := decode(t, `[]`).([]any)
	if !ok || list == nil {
		t.Fatalf("decoded %#v, want an empty, non-nil list", list)
	}
	if encoded, _ := apikit.Marshal(list); string(encoded) != "[]" {
		t.Errorf("Marshal = %s, want []", encoded)
	}
}

func TestDecodeRejectsMalformedInput(t *testing.T) {
	for _, text := range []string{``, `{`, `{"a":}`, `[1,`, `{"a":1} {"b":2}`, `1 2`, `{"a":1}x`, `nope`} {
		if value, err := apikit.Unmarshal([]byte(text)); err == nil {
			t.Errorf("Unmarshal(%q) = %v, want an error", text, value)
		}
	}
}

func TestObjectOperations(t *testing.T) {
	object := apikit.NewObject()
	object.Set("b", "1")
	object.Set("a", "2")
	object.Set("b", "3") // keeps its position
	if got, want := object.Keys(), []string{"b", "a"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
	if value, ok := object.Get("b"); !ok || value != "3" {
		t.Errorf(`Get("b") = %v, %v`, value, ok)
	}
	if object.Has("missing") {
		t.Error(`Has("missing") = true`)
	}

	keys := object.Keys()
	keys[0] = "changed"
	if object.Keys()[0] != "b" {
		t.Error("Keys() hands out the object's own slice")
	}

	object.Delete("b")
	object.Delete("missing")
	if got, want := object.Keys(), []string{"a"}; !slices.Equal(got, want) {
		t.Errorf("Keys() after Delete = %v, want %v", got, want)
	}
	if object.Len() != 1 {
		t.Errorf("Len() = %d, want 1", object.Len())
	}

	var seen []string
	for key, value := range object.All() {
		seen = append(seen, key+"="+value.(string))
	}
	if !slices.Equal(seen, []string{"a=2"}) {
		t.Errorf("All() yielded %v", seen)
	}
}

func TestNilAndZeroObject(t *testing.T) {
	var none *apikit.Object
	if _, ok := none.Get("a"); ok || none.Has("a") || none.Len() != 0 || none.Keys() != nil {
		t.Error("a nil object is not empty")
	}
	none.Delete("a")
	for range none.All() {
		t.Error("a nil object yields a key")
	}
	if encoded, err := apikit.Marshal(none); err != nil || string(encoded) != "null" {
		t.Errorf("Marshal(nil object) = %s, %v", encoded, err)
	}

	var zero apikit.Object
	zero.Set("a", true)
	if value, ok := zero.Get("a"); !ok || value != true {
		t.Error("the zero Object cannot be written to")
	}
}

func TestObjectUnmarshalJSON(t *testing.T) {
	var holder struct {
		Payload *apikit.Object `json:"payload"`
	}
	if err := json.Unmarshal([]byte(`{"payload":{"b":1,"a":2}}`), &holder); err != nil {
		t.Fatal(err)
	}
	if got, want := holder.Payload.Keys(), []string{"b", "a"}; !slices.Equal(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
	if err := json.Unmarshal([]byte(`{"payload":[1]}`), &holder); err == nil {
		t.Error("a list was accepted where an object belongs")
	}
}

func TestMarshalLeavesHTMLCharactersAlone(t *testing.T) {
	object := apikit.NewObject()
	object.Set("<k&>", "<v&>")
	encoded, err := apikit.Marshal([]any{object, "a<b"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `[{"<k&>":"<v&>"},"a<b"]`; got != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

func TestNormalize(t *testing.T) {
	type point struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	value, err := apikit.Normalize(map[string]any{
		"zeta":   int64(7),
		"alpha":  []any{1.5, uint8(2), "s", nil, true},
		"nested": map[string]any{"b": 1, "a": json.Number("2")},
		"struct": point{X: 1, Y: 2},
		"typed":  []string{"x", "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := apikit.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"alpha":[1.5,2,"s",null,true],"nested":{"a":2,"b":1},"struct":{"x":1,"y":2},"typed":["x","y"],"zeta":7}`
	if string(encoded) != want {
		t.Errorf("Normalize produced\n got %s\nwant %s", encoded, want)
	}

	existing := decode(t, `{"b":[1,{"a":null}],"a":2}`)
	again, err := apikit.Normalize(existing)
	if err != nil {
		t.Fatal(err)
	}
	if !apikit.Equal(existing, again) || !slices.Equal(again.(*apikit.Object).Keys(), []string{"b", "a"}) {
		t.Error("Normalize changed a value that already had the decoded types")
	}

	for name, bad := range map[string]any{"NaN": math.NaN(), "channel": make(chan int), "nested NaN": []any{math.Inf(1)}} {
		if _, err := apikit.Normalize(bad); err == nil {
			t.Errorf("Normalize(%s) returned no error", name)
		}
	}
}

func TestCloneIsIndependent(t *testing.T) {
	original := decode(t, `{"list":[1,{"deep":"x"}],"name":"n"}`).(*apikit.Object)
	clone := apikit.Clone(original).(*apikit.Object)
	if !apikit.Equal(original, clone) {
		t.Fatal("the clone differs from the original")
	}

	clone.Set("name", "changed")
	clone.Set("extra", true)
	list, _ := clone.Get("list")
	list.([]any)[1].(*apikit.Object).Set("deep", "changed")
	list.([]any)[0] = "changed"

	encoded, _ := apikit.Marshal(original)
	if got, want := string(encoded), `{"list":[1,{"deep":"x"}],"name":"n"}`; got != want {
		t.Errorf("changing the clone changed the original: %s", got)
	}

	if apikit.Clone(nil) != nil || apikit.Clone("s") != "s" {
		t.Error("Clone changed a scalar")
	}
	if cloned := apikit.Clone((*apikit.Object)(nil)); cloned.(*apikit.Object) != nil {
		t.Error("Clone turned a nil object into something")
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{`null`, `null`, true},
		{`null`, `false`, false},
		{`true`, `true`, true},
		{`true`, `1`, false},
		{`"a"`, `"a"`, true},
		{`"1"`, `1`, false},
		{`1`, `1.0`, true},
		{`1`, `1e0`, true},
		{`100`, `1e2`, true},
		{`0.1`, `0.10`, true},
		{`1`, `2`, false},
		{`12345678901234567890`, `12345678901234567891`, false},
		{`12345678901234567890`, `12345678901234567890.0`, true},
		{`[1,2]`, `[1,2]`, true},
		{`[1,2]`, `[2,1]`, false},
		{`[1,2]`, `[1,2,3]`, false},
		{`[]`, `{}`, false},
		{`{"a":1,"b":[true]}`, `{"b":[true],"a":1.0}`, true},
		{`{"a":1}`, `{"a":1,"b":null}`, false},
		{`{"a":1}`, `{"b":1}`, false},
		{`{"a":{"x":"y"}}`, `{"a":{"x":"z"}}`, false},
	}
	for _, tt := range tests {
		a, b := decode(t, tt.a), decode(t, tt.b)
		if got := apikit.Equal(a, b); got != tt.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := apikit.Equal(b, a); got != tt.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tt.b, tt.a, got, tt.want)
		}
	}
}

func TestText(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{`null`, ""},
		{`true`, "true"},
		{`false`, "false"},
		{`"plain text"`, "plain text"},
		{`""`, ""},
		{`12`, "12"},
		{`1.0`, "1.0"},
		{`1e3`, "1e3"},
		{`12345678901234567890`, "12345678901234567890"},
		{`[]`, "[]"},
		{`{}`, "{}"},
		{`["a",1,true,null]`, `["a", 1, true, null]`},
		{`{"b":1,"a":["x",{"y":"z"}]}`, `{"b": 1, "a": ["x", {"y": "z"}]}`},
		{`{"text":"Störung <a&b> \"q\""}`, `{"text": "Störung <a&b> \"q\""}`},
	}
	for _, tt := range tests {
		if got := apikit.Text(decode(t, tt.source)); got != tt.want {
			t.Errorf("Text(%s) = %q, want %q", tt.source, got, tt.want)
		}
	}
	if got := apikit.Text((*apikit.Object)(nil)); got != "null" {
		t.Errorf("Text(nil object) = %q, want null", got)
	}
	if got := apikit.Text(42); got != "42" {
		t.Errorf("Text(42) = %q, want 42", got)
	}
}

func TestFalsy(t *testing.T) {
	falsy := []string{`null`, `false`, `""`, `0`, `0.0`, `-0`, `0e5`, `[]`, `{}`}
	truthy := []string{`true`, `"0"`, `" "`, `1`, `-1`, `0.001`, `[0]`, `[null]`, `{"a":null}`}
	for _, source := range falsy {
		if !apikit.Falsy(decode(t, source)) {
			t.Errorf("Falsy(%s) = false, want true", source)
		}
	}
	for _, source := range truthy {
		if apikit.Falsy(decode(t, source)) {
			t.Errorf("Falsy(%s) = true, want false", source)
		}
	}
	if !apikit.Falsy((*apikit.Object)(nil)) {
		t.Error("a nil object is not falsy")
	}
}

func TestDecodeFromReaderStopsAtTheValue(t *testing.T) {
	value, err := apikit.Decode(strings.NewReader("  [1, 2]\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := apikit.Text(value); got != "[1, 2]" {
		t.Errorf("decoded %s", got)
	}
}
