package definition

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/goccy/go-yaml"

	apikit "github.com/mittwald/api-kit"
)

// Source is one definition document: the base definition a tool embeds, or
// an overlay a user wrote.
type Source struct {
	// Name says where the document comes from, usually a file path. It is
	// used in messages only.
	Name string
	// Data is the document as YAML. JSON is YAML too.
	Data []byte
}

// parse reads a YAML document into the value types of package apikit:
// mappings become [*apikit.Object] in document order, numbers become
// [json.Number]. An empty document is an empty mapping.
func parse(source Source) (*apikit.Object, error) {
	var document any
	if err := yaml.UnmarshalWithOptions(source.Data, &document, yaml.UseOrderedMap()); err != nil {
		return nil, apikit.Errorf(apikit.KindDefinition, "cannot read definition %s: %w", source.Name, err)
	}
	if document == nil {
		return apikit.NewObject(), nil
	}
	value, err := convert(document, "")
	if err != nil {
		return nil, apikit.Errorf(apikit.KindDefinition, "cannot read definition %s: %w", source.Name, err)
	}
	object, ok := value.(*apikit.Object)
	if !ok {
		return nil, apikit.Errorf(apikit.KindDefinition,
			"cannot read definition %s: the document must be a mapping, not %s", source.Name, kindOf(value))
	}
	return object, nil
}

// convert turns what the YAML decoder produced into apikit values.
func convert(v any, path string) (any, error) {
	switch value := v.(type) {
	case nil, bool, string:
		return value, nil
	case yaml.MapSlice:
		object := apikit.NewObject()
		for _, item := range value {
			// The decoder hands out every key as a string, a number or a
			// boolean used as a key included.
			key := fmt.Sprint(item.Key)
			converted, err := convert(item.Value, join(path, key))
			if err != nil {
				return nil, err
			}
			object.Set(key, converted)
		}
		return object, nil
	case []any:
		list := make([]any, len(value))
		for i, item := range value {
			converted, err := convert(item, index(path, i))
			if err != nil {
				return nil, err
			}
			list[i] = converted
		}
		return list, nil
	case int:
		return json.Number(strconv.Itoa(value)), nil
	case int64:
		return json.Number(strconv.FormatInt(value, 10)), nil
	case uint64:
		return json.Number(strconv.FormatUint(value, 10)), nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%s: %v is not a number a definition can hold", orRoot(path), value)
		}
		return json.Number(strconv.FormatFloat(value, 'g', -1, 64)), nil
	}
	return nil, fmt.Errorf("%s: unsupported value of type %T", orRoot(path), v)
}

func orRoot(path string) string {
	if path == "" {
		return "document"
	}
	return path
}

// kindOf names the type of a definition value the way a YAML author thinks
// of it.
func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "nothing"
	case bool:
		return "a boolean"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case []any:
		return "a list"
	case *apikit.Object:
		return "a mapping"
	}
	return fmt.Sprintf("%T", v)
}
