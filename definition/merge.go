package definition

import (
	"fmt"
	"slices"
	"strings"

	apikit "github.com/mittwald/api-kit"
)

// Mode says how an overlay is merged into the definition below it.
type Mode uint8

// The merge modes.
const (
	// Additive lets an overlay add to the definition but never change it:
	// new keys are taken over, lists gain the overlay's extra entries, and
	// a value the definition already has keeps it. An overlay written for
	// an older definition can then not shadow what a newer one changed.
	Additive Mode = iota
	// Override lets an overlay replace values: every value that is not a
	// mapping, lists included, is replaced as a whole.
	Override
)

// Merge returns base with overlay merged into it; neither argument is
// changed. Mappings are always merged key by key, so an overlay cannot
// remove a key. What happens to other values depends on mode.
//
// In [Additive] mode a setter of an overlay that clashes with a setter of
// the same resource in base is left out, the way a key keeps its value: a
// stale copy would otherwise become a second definition. A value whose type
// differs from the one in base (a list where a string is, say) is ignored
// and reported as a warning. source names the overlay in those warnings.
func Merge(base, overlay *apikit.Object, mode Mode, source string) (*apikit.Object, []Diagnostic) {
	m := merger{mode: mode, source: source}
	merged := m.merge(base, overlay, nil)
	object, _ := merged.(*apikit.Object)
	return object, m.warnings
}

type merger struct {
	mode     Mode
	source   string
	warnings []Diagnostic
}

func (m *merger) merge(base, overlay any, path []string) any {
	baseObject, baseIsObject := base.(*apikit.Object)
	overlayObject, overlayIsObject := overlay.(*apikit.Object)
	if baseIsObject && overlayIsObject {
		merged := apikit.NewObject()
		for key, value := range baseObject.All() {
			merged.Set(key, apikit.Clone(value))
		}
		for key, value := range overlayObject.All() {
			if existing, ok := merged.Get(key); ok {
				merged.Set(key, m.merge(existing, value, append(slices.Clip(path), key)))
			} else {
				merged.Set(key, apikit.Clone(value))
			}
		}
		return merged
	}
	if m.mode == Override {
		return apikit.Clone(overlay)
	}

	baseList, baseIsList := base.([]any)
	overlayList, overlayIsList := overlay.([]any)
	if baseIsList && overlayIsList {
		merged := slices.Clone(baseList)
		isSetters := isSettersPath(path)
		for _, entry := range overlayList {
			if slices.ContainsFunc(merged, func(existing any) bool { return apikit.Equal(existing, entry) }) {
				continue
			}
			if isSetters && slices.ContainsFunc(baseList, func(existing any) bool { return settersClash(existing, entry) }) {
				continue
			}
			merged = append(merged, apikit.Clone(entry))
		}
		return merged
	}

	// A list or mapping cannot absorb a scalar, or the other way round. This
	// is a mistake rather than an edit, so it is reported. A scalar over a
	// scalar is the ordinary case of an overlay that may only add.
	if baseIsObject || overlayIsObject || baseIsList || overlayIsList {
		m.warnings = append(m.warnings, Diagnostic{
			Severity: Warning,
			Source:   m.source,
			Path:     strings.Join(path, "."),
			Message: fmt.Sprintf("ignored: the definition has %s here, the overlay %s; only an overlay merged in override mode can replace it",
				kindOf(base), kindOf(overlay)),
		})
	}
	return base
}

// isSettersPath reports whether path is resources.<name>.setters.
func isSettersPath(path []string) bool {
	return len(path) == 3 && path[0] == keyResources && path[2] == keySetters
}

// settersClash reports whether two setters of one resource cannot both be
// defined. An alias must not be a setter name, and only setters of one name
// may share an alias. Setters that share a name form one call, which runs
// those whose parameters were given: each needs parameters, none may share
// one, and they may share a step parameter only with the same definition.
func settersClash(first, second any) bool {
	a, okA := first.(*apikit.Object)
	b, okB := second.(*apikit.Object)
	if !okA || !okB {
		return false
	}
	nameA, _ := a.Get(keyName)
	nameB, _ := b.Get(keyName)
	aliasesA, aliasesB := rawAliases(a), rawAliases(b)
	if containsValue(aliasesB, nameA) || containsValue(aliasesA, nameB) {
		return true
	}
	if !apikit.Equal(nameA, nameB) {
		return intersects(aliasesA, aliasesB)
	}
	optionsA, optionsB := rawOptionNames(a, keyParameters), rawOptionNames(b, keyParameters)
	stepsA, stepsB := rawStepParameters(a), rawStepParameters(b)
	if len(optionsA) == 0 || len(optionsB) == 0 || intersects(optionsA, optionsB) {
		return true
	}
	for name, definition := range stepsA {
		if slices.Contains(optionsB, name) {
			return true
		}
		if other, ok := stepsB[name]; ok && !apikit.Equal(definition, other) {
			return true
		}
	}
	for name := range stepsB {
		if slices.Contains(optionsA, name) {
			return true
		}
	}
	return false
}

// rawAliases returns the aliases of a setter or a parameter, ignoring
// anything that is not a string. Merging happens before the document is
// checked, so it has to cope with any shape.
func rawAliases(object *apikit.Object) []string {
	value, _ := object.Get(keyAliases)
	list, _ := value.([]any)
	var out []string
	for _, item := range list {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// rawOptionNames returns the names and aliases of the parameters listed
// under key.
func rawOptionNames(setter *apikit.Object, key string) []string {
	value, _ := setter.Get(key)
	list, _ := value.([]any)
	var out []string
	for _, item := range list {
		parameter, ok := item.(*apikit.Object)
		if !ok {
			continue
		}
		if name, ok := parameter.Get(keyName); ok {
			if text, ok := name.(string); ok {
				out = append(out, text)
			}
		}
		out = append(out, rawAliases(parameter)...)
	}
	return out
}

// rawStepParameters returns the definition of each step parameter by every
// name it is given with.
func rawStepParameters(setter *apikit.Object) map[string]*apikit.Object {
	value, _ := setter.Get(keyStepParameters)
	list, _ := value.([]any)
	out := map[string]*apikit.Object{}
	for _, item := range list {
		parameter, ok := item.(*apikit.Object)
		if !ok {
			continue
		}
		if name, ok := parameter.Get(keyName); ok {
			if text, ok := name.(string); ok {
				out[text] = parameter
			}
		}
		for _, alias := range rawAliases(parameter) {
			out[alias] = parameter
		}
	}
	return out
}

func containsValue(list []string, value any) bool {
	text, ok := value.(string)
	return ok && slices.Contains(list, text)
}

func intersects(a, b []string) bool {
	return slices.ContainsFunc(a, func(item string) bool { return slices.Contains(b, item) })
}
