package apikit

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Text returns a value of the types [Decode] produces as a person or a
// template sees it: nothing for null, true or false for a boolean, a string
// as it is, a number as it was spelled, and a list or an object as one line
// of JSON with a space after every comma and colon.
func Text(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case bool:
		if value {
			return "true"
		}
		return "false"
	case string:
		return value
	case json.Number:
		return value.String()
	case []any, *Object:
		var buf bytes.Buffer
		writeSpacedJSON(&buf, value)
		return buf.String()
	}
	return fmt.Sprint(v)
}

// writeSpacedJSON writes v as one line of JSON with ", " between items and
// ": " after keys.
func writeSpacedJSON(buf *bytes.Buffer, v any) {
	switch value := v.(type) {
	case []any:
		buf.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				buf.WriteString(", ")
			}
			writeSpacedJSON(buf, item)
		}
		buf.WriteByte(']')
	case *Object:
		if value == nil {
			buf.WriteString("null")
			return
		}
		buf.WriteByte('{')
		first := true
		for key, item := range value.All() {
			if !first {
				buf.WriteString(", ")
			}
			first = false
			writeSpacedJSON(buf, key)
			buf.WriteString(": ")
			writeSpacedJSON(buf, item)
		}
		buf.WriteByte('}')
	default:
		if err := encodeJSON(buf, value); err != nil {
			// Only a value outside the decoded types gets here; show it
			// rather than lose it.
			fmt.Fprintf(buf, "%q", fmt.Sprint(value))
		}
	}
}

// Falsy reports whether a value of the types [Decode] produces counts as
// "nothing there": null, false, the empty string, a number equal to zero, an
// empty list or an empty object.
func Falsy(v any) bool {
	switch value := v.(type) {
	case nil:
		return true
	case bool:
		return !value
	case string:
		return value == ""
	case json.Number:
		number, ok := parseNumber(value)
		return ok && number.Sign() == 0
	case []any:
		return len(value) == 0
	case *Object:
		return value.Len() == 0
	}
	return false
}
