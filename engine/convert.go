package engine

import (
	"fmt"
	"os"
	"slices"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// convertParameters turns what was given for the parameters of a call into
// what the call sends: the value of a parameter of type time becomes the
// time it stands for, and the files of a parameter of type file must exist.
// origin says where the values were given and may be empty.
func (e *Engine) convertParameters(call *definition.Call, args values, origin string) error {
	if origin != "" {
		origin += ": "
	}
	for _, parameter := range slices.Concat(call.Parameters, call.StepParameters) {
		value, ok := args.get(parameter.Name)
		if !ok {
			continue
		}
		switch parameter.Type {
		case definition.TypeTime:
			converted, err := e.parameterTime(parameter, apikit.Text(value))
			if err != nil {
				return apikit.Errorf(apikit.KindUsage, "%s--%s %w", origin, parameter.Name, err)
			}
			args.set(parameter.Name, converted)
		case definition.TypeFile:
			for _, path := range listOf(value) {
				if err := checkFile(apikit.Text(path)); err != nil {
					return apikit.Errorf(apikit.KindUsage, "%s--%s %w", origin, parameter.Name, err)
				}
			}
		}
	}
	return nil
}

// parameterTime returns the time a parameter of type time stands for, as
// RFC 3339 text in its time zone: a preset of the parameter, or a time
// [definition.ParseTime] reads.
func (e *Engine) parameterTime(parameter *definition.Parameter, text string) (string, error) {
	zone := time.Local
	if parameter.Timezone != "" {
		var err error
		if zone, err = time.LoadLocation(parameter.Timezone); err != nil {
			return "", fmt.Errorf("has an unknown time zone %q", parameter.Timezone)
		}
	}
	for _, preset := range parameter.Presets {
		if preset.Key == text {
			text = preset.Value
			break
		}
	}
	parsed, err := definition.ParseTime(text, e.now().In(zone))
	if err != nil {
		return "", err
	}
	// Fractions of a second that were given are kept.
	return parsed.Format(time.RFC3339Nano), nil
}

// checkFile reports a path that is no file that can be read.
func checkFile(path string) error {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fmt.Errorf("%s: no such file", path)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%s cannot be read", path)
	}
	return file.Close()
}

// formatWarnings returns what the checks of the tool find wrong with the
// values of the parameters that name a format, each line led by the
// option.
func (e *Engine) formatWarnings(setters []*definition.Setter, args values) []string {
	var warnings []string
	seen := map[string]bool{}
	for _, setter := range setters {
		for _, parameter := range setter.Parameters {
			check := e.formats[parameter.Format]
			value, ok := args.get(parameter.Name)
			text, isText := value.(string)
			if check == nil || !ok || !isText || seen[parameter.Name] {
				continue
			}
			seen[parameter.Name] = true
			for _, mistake := range check(text) {
				warnings = append(warnings, "--"+parameter.Name+": "+mistake)
			}
		}
	}
	return warnings
}

// listOf returns a value as a list: a list as it is, nothing as none, and
// anything else as a list of one.
func listOf(value any) []any {
	switch value := value.(type) {
	case nil:
		return nil
	case []any:
		return value
	}
	return []any{value}
}
