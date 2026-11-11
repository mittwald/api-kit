package apikit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"reflect"
	"slices"
	"strconv"
)

var objectType = reflect.TypeFor[Object]()

// Decode reads one JSON value from r. Objects become [*Object] and keep the
// order of their keys, numbers become [json.Number] and keep their exact
// spelling, arrays become []any (never nil). Anything but white space after
// the value is an error.
func Decode(r io.Reader) (any, error) {
	decoder := json.NewDecoder(r)
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after the JSON value")
		}
		return nil, err
	}
	return value, nil
}

// Unmarshal decodes one JSON value from data, as [Decode] does.
func Unmarshal(data []byte) (any, error) {
	return Decode(bytes.NewReader(data))
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		// nil, bool, string or json.Number.
		return token, nil
	}
	switch delim {
	case '{':
		object := NewObject()
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("unexpected %v where an object key belongs", keyToken)
			}
			value, err := decodeValue(decoder)
			if err != nil {
				return nil, err
			}
			object.Set(key, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		list := []any{}
		for decoder.More() {
			value, err := decodeValue(decoder)
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return list, nil
	}
	return nil, fmt.Errorf("unexpected %q in JSON", rune(delim))
}

// Marshal returns the compact JSON encoding of v. Unlike json.Marshal it
// leaves <, > and & as they are: the result is sent to an API or shown in a
// terminal, not embedded in HTML.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeJSON(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Normalize converts a value built from ordinary Go types into the types
// [Decode] produces: every integer and floating-point type becomes a
// [json.Number], map[string]any becomes an [*Object] with its keys sorted,
// and slices become []any. Values that already have those types are returned
// as they are, lists and objects are converted in depth. It returns an error
// for a value JSON cannot hold, such as a channel or a NaN.
func Normalize(v any) (any, error) {
	switch value := v.(type) {
	case nil, bool, string, json.Number:
		return value, nil
	case *Object:
		if value == nil {
			return nil, nil
		}
		out := NewObject()
		for key, item := range value.All() {
			normalized, err := Normalize(item)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out.Set(key, normalized)
		}
		return out, nil
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			normalized, err := Normalize(item)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = normalized
		}
		return out, nil
	case map[string]any:
		out := NewObject()
		for _, key := range slices.Sorted(maps.Keys(value)) {
			normalized, err := Normalize(value[key])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out.Set(key, normalized)
		}
		return out, nil
	}
	// Everything else (sized integers, floats, typed slices and maps,
	// structs) takes the detour through its JSON encoding.
	data, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	return Unmarshal(data)
}

// Clone returns a deep copy of a value of the types [Decode] produces.
// Scalars are returned as they are.
func Clone(v any) any {
	switch value := v.(type) {
	case *Object:
		if value == nil {
			return value
		}
		out := &Object{keys: slices.Clone(value.keys), values: make(map[string]any, len(value.values))}
		for key, item := range value.values {
			out.values[key] = Clone(item)
		}
		return out
	case []any:
		if value == nil {
			return value
		}
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = Clone(item)
		}
		return out
	}
	return v
}

// Equal reports whether two values of the types [Decode] produces are the
// same data: objects have the same keys with equal values, whatever their
// order; lists have equal elements in the same order; numbers are compared
// by value, so 1, 1.0 and 1e0 are equal.
func Equal(a, b any) bool {
	switch left := a.(type) {
	case nil:
		return b == nil
	case bool:
		right, ok := b.(bool)
		return ok && left == right
	case string:
		right, ok := b.(string)
		return ok && left == right
	case json.Number:
		right, ok := b.(json.Number)
		return ok && numbersEqual(left, right)
	case []any:
		right, ok := b.([]any)
		return ok && slices.EqualFunc(left, right, Equal)
	case *Object:
		right, ok := b.(*Object)
		if !ok || left.Len() != right.Len() {
			return false
		}
		for key, value := range left.All() {
			other, ok := right.Get(key)
			if !ok || !Equal(value, other) {
				return false
			}
		}
		return true
	}
	return false
}

// numbersEqual compares two JSON numbers by value. Integers of any size are
// compared exactly; anything else is compared with far more precision than
// a float64 holds.
func numbersEqual(a, b json.Number) bool {
	if a == b {
		return true
	}
	if x, err := strconv.ParseInt(a.String(), 10, 64); err == nil {
		if y, err := strconv.ParseInt(b.String(), 10, 64); err == nil {
			return x == y
		}
	}
	x, okX := parseNumber(a)
	y, okY := parseNumber(b)
	return okX && okY && x.Cmp(y) == 0
}

func parseNumber(n json.Number) (*big.Float, bool) {
	const precision = 512
	value, _, err := big.ParseFloat(n.String(), 10, precision, big.ToNearestEven)
	if err != nil {
		return nil, false
	}
	return value, true
}

// jsonKind names the JSON type of a decoded value for error messages.
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case json.Number:
		return "number"
	case []any:
		return "array"
	case *Object:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}
