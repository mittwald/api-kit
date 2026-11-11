package apikit

import (
	"bytes"
	"encoding/json"
	"iter"
	"slices"
)

// Object is a JSON object that keeps its keys in insertion order. API
// responses, payloads and definition documents are held as Objects, because
// the order of keys shows up in output and decides which of several rules
// applies first.
//
// The values of an Object, and of the lists inside it, are of the types
// [Decode] produces: nil, bool, string, [json.Number], []any and *Object.
//
// An Object is not safe for concurrent use while it is modified.
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object {
	return &Object{values: map[string]any{}}
}

// Get returns the value of key and whether the key is present. It is safe to
// call on a nil Object, which has no keys.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	value, ok := o.values[key]
	return value, ok
}

// Has reports whether key is present.
func (o *Object) Has(key string) bool {
	_, ok := o.Get(key)
	return ok
}

// Set stores value under key. A new key is appended; an existing key keeps
// its position.
func (o *Object) Set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// Delete removes key. Deleting a key that is not present does nothing.
func (o *Object) Delete(key string) {
	if o == nil {
		return
	}
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

// Len returns the number of keys. A nil Object has none.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Keys returns the keys in insertion order. The slice is the caller's.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return slices.Clone(o.keys)
}

// All iterates over the keys and values in insertion order. The object must
// not be modified during the iteration.
func (o *Object) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if o == nil {
			return
		}
		for _, key := range o.keys {
			if !yield(key, o.values[key]) {
				return
			}
		}
	}
}

// MarshalJSON encodes the object with its keys in insertion order.
func (o *Object) MarshalJSON() ([]byte, error) {
	if o == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := encodeJSON(&buf, key); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := encodeJSON(&buf, o.values[key]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON decodes a JSON object, keeping the order of its keys. A key
// that occurs twice keeps its first position and its last value.
func (o *Object) UnmarshalJSON(data []byte) error {
	value, err := Unmarshal(data)
	if err != nil {
		return err
	}
	object, ok := value.(*Object)
	if !ok {
		return &json.UnmarshalTypeError{Value: jsonKind(value), Type: objectType}
	}
	*o = *object
	return nil
}

// encodeJSON appends the compact JSON encoding of v, leaving <, > and & as
// they are.
func encodeJSON(buf *bytes.Buffer, v any) error {
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return err
	}
	// Encode ends every value with a newline.
	buf.Truncate(buf.Len() - 1)
	return nil
}
