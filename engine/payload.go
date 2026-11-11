package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// quoted is a string of a payload template that is written as JSON text.
// What stands between quotes there is a string in the payload, whatever
// its placeholders hold, while a string of a payload template written as a
// mapping passes the value of a placeholder that stands alone on as it is.
type quoted string

// bareMark starts the strings that stand in for the placeholders outside
// the strings of a payload template while it is read as JSON.
const bareMark = "\x00"

// parsePayload reads a payload template that is written as JSON text into
// the form of one written as a mapping, so that values are filled into
// its strings and never into the text: a quote in a value cannot break
// the payload then, and a value cannot add a key to it. A placeholder
// between quotes belongs to a string. One outside of them, as in
// {"age": ${age}}, stands for a value of whatever type.
func parsePayload(source string) (any, error) {
	var text strings.Builder
	var bare []string
	inString := false
	for i := 0; i < len(source); i++ {
		switch c := source[i]; {
		case inString && c == '\\' && i+1 < len(source):
			text.WriteString(source[i : i+2])
			i++
		case c == '"':
			inString = !inString
			text.WriteByte(c)
		case !inString && strings.HasPrefix(source[i:], "${"):
			end := strings.IndexByte(source[i:], '}')
			if end < len("${x") {
				return nil, errors.New("a placeholder outside a string has no name or no closing brace")
			}
			fmt.Fprintf(&text, `"\u0000%d"`, len(bare))
			bare = append(bare, source[i:i+end+1])
			i += end
		default:
			text.WriteByte(c)
		}
	}
	value, err := apikit.Unmarshal([]byte(text.String()))
	if err != nil {
		return nil, err
	}
	// Every stand-in is found once, unless a string of the template reads
	// like one.
	found := make([]int, len(bare))
	tree := markPayload(value, bare, found)
	if slices.ContainsFunc(found, func(count int) bool { return count != 1 }) {
		return nil, errors.New("one of its strings reads like a placeholder outside a string")
	}
	return tree, nil
}

// markPayload returns a payload template that was read as JSON with its
// strings told apart: the stand-in of a placeholder becomes the
// placeholder, every other string is [quoted].
func markPayload(value any, bare []string, found []int) any {
	placeholder := func(text string) (string, bool) {
		digits, marked := strings.CutPrefix(text, bareMark)
		index, err := strconv.Atoi(digits)
		if !marked || err != nil || index < 0 || index >= len(bare) {
			return "", false
		}
		found[index]++
		return bare[index], true
	}
	switch value := value.(type) {
	case string:
		if name, ok := placeholder(value); ok {
			return name
		}
		return quoted(value)
	case []any:
		for i, item := range value {
			value[i] = markPayload(item, bare, found)
		}
	case *apikit.Object:
		marked := apikit.NewObject()
		for key, item := range value.All() {
			if name, ok := placeholder(key); ok {
				key = name
			}
			marked.Set(key, markPayload(item, bare, found))
		}
		return marked
	}
	return value
}

// payloadTemplate returns the payload template of a setter as a mapping or
// a list: nil for a setter without one, and one written as JSON text read
// once per call.
func (r *callRun) payloadTemplate(setter *definition.Setter) (any, error) {
	source, isText := setter.PayloadTemplate.(string)
	switch {
	case !isText:
		return setter.PayloadTemplate, nil
	case strings.TrimSpace(source) == "":
		return nil, nil
	}
	if tree, ok := r.payloads[setter]; ok {
		return tree, nil
	}
	tree, err := parsePayload(source)
	if err != nil {
		return nil, apikit.Errorf(apikit.KindDefinition,
			"the payload template of setter %s of %s is no JSON with placeholders: %w", setter.Name, r.resource, err)
	}
	r.payloads[setter] = tree
	return tree, nil
}

// payloadBuild fills the values of a call into a payload template.
type payloadBuild struct {
	// args are the values the placeholders stand for.
	args values
	// optional are the parameters that may be left out, and whose
	// placeholders then take their part of the payload with them.
	optional []string
	// used holds every name the template asked for, with its value or
	// with nothing: the message of the setter may name them too.
	used values
	// missing are the names without a value that are not optional.
	missing []string
}

// node returns the part of the payload a part of the template stands for,
// and whether it is kept. A part goes when it depends on an optional
// parameter that was not given, and a list or an object goes when all its
// parts went. An empty list or object that the template spells out stays.
func (b *payloadBuild) node(template any) (value any, keep bool) {
	switch template := template.(type) {
	case string:
		return b.text(template, true)
	case quoted:
		return b.text(string(template), false)
	case []any:
		list := make([]any, 0, len(template))
		for _, item := range template {
			if value, keep := b.node(item); keep {
				list = append(list, value)
			}
		}
		return list, len(list) > 0 || len(template) == 0
	case *apikit.Object:
		object := apikit.NewObject()
		for key, item := range template.All() {
			value, keep := b.node(item)
			if !keep {
				continue
			}
			// A key is a template too, and one that depends on a
			// parameter that was not given goes with its value.
			if name, keep := b.text(key, false); keep {
				object.Set(apikit.Text(name), value)
			}
		}
		return object, object.Len() > 0 || template.Len() == 0
	}
	return template, true
}

// text returns what a string of the template stands for. typed lets a
// string that is one placeholder and nothing else pass the value on as it
// is, so that a flag arrives as a boolean and a list as an array.
func (b *payloadBuild) text(source string, typed bool) (value any, keep bool) {
	template := expr.ParseTemplate(source)
	keep = true
	for _, placeholder := range template.Placeholders() {
		// A plain $name is text unless it has a value.
		if !placeholder.Braced {
			continue
		}
		name := placeholder.Name
		value, given := b.args.get(name)
		if !given {
			value = ""
		}
		b.used.set(name, value)
		switch {
		case given:
		case slices.ContainsFunc(b.optional, func(optional string) bool { return variableKey(optional) == variableKey(name) }):
			keep = false
		case !slices.Contains(b.missing, name):
			b.missing = append(b.missing, name)
		}
	}
	if !keep {
		return nil, false
	}
	if name, sole := template.Sole(); sole && typed {
		value, _ := b.args.get(name)
		return apikit.Clone(value), true
	}
	return renderWith(source, b.args.get), true
}

// payload returns the body of the request of a setter, nil for a setter
// that sends none, and what its template asked for. vals are the values
// of the setter. A payload that needs a value nobody gave, and one of
// which nothing is left, is an error: the request would not say what was
// meant.
func (r *callRun) payload(setter *definition.Setter, vals values) (body any, used values, err error) {
	used = values{}
	template, err := r.payloadTemplate(setter)
	if err != nil || template == nil {
		return nil, used, err
	}
	build := payloadBuild{args: vals, used: used}
	for _, parameter := range setter.Parameters {
		if !parameter.Required {
			build.optional = append(build.optional, parameter.Name)
		}
	}
	body, keep := build.node(template)
	switch {
	case len(build.missing) > 0:
		return nil, used, apikit.Errorf(apikit.KindUsage, "%s cannot send its payload without a value for %s",
			r.call.Name, strings.Join(build.missing, ", "))
	case !keep:
		return nil, used, apikit.Errorf(apikit.KindUsage, "%s needs at least one of %s", r.call.Name, optionList(build.optional))
	}
	return body, used, nil
}

// checkPayload reports a payload that cannot be built from vals, the
// values of the setter before its steps ran. The steps stand in with a
// placeholder, since each of them yields a value or fails.
func (r *callRun) checkPayload(setter *definition.Setter, vals values) error {
	probe := maps.Clone(vals)
	for _, step := range setter.Steps {
		probe.set(step.As, "-")
	}
	if !probe.has(definition.VariableID) {
		probe.set(definition.VariableID, "-")
	}
	_, _, err := r.payload(setter, probe)
	return err
}
