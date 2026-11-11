package cli

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// load reads the definition and the overlays that exist, merges and checks
// them. Parts of overlays that were ignored are printed as warnings.
func (a *App) load(root *cobra.Command, override bool) (*definition.Document, error) {
	options := definition.LoadOptions{Reserved: reserved(root)}
	if override {
		options.Mode = definition.Override
	}
	for _, overlay := range a.opts.Overlays {
		data, err := os.ReadFile(overlay.Path)
		if errors.Is(err, fs.ErrNotExist) && overlay.Optional {
			continue
		}
		if err != nil {
			return nil, apikit.Errorf(apikit.KindDefinition, "cannot read definition %s: %w", overlay.Path, err)
		}
		options.Overlays = append(options.Overlays, definition.Source{Name: overlay.Path, Data: data})
	}
	doc, warnings, err := definition.Load(a.opts.Definition, options)
	for _, warning := range warnings {
		writeLine(a.opts.Streams.Err, "WARNING: "+warning.Source+": "+warning.String())
	}
	if err != nil {
		return nil, err
	}
	a.tune(&doc.HTTP)
	return doc, nil
}

// tune applies the environment variables that override the HTTP settings of
// the definition. A value that is no number, or too small, is ignored with
// a warning, and the definition's value stays.
func (a *App) tune(settings *definition.HTTP) {
	if a.opts.EnvPrefix == "" {
		return
	}
	number := func(suffix string, minimum float64, whole bool) (float64, bool) {
		name := a.opts.EnvPrefix + "_" + suffix
		raw := strings.TrimSpace(a.opts.Getenv(name))
		if raw == "" {
			return 0, false
		}
		value, err := strconv.ParseFloat(raw, 64)
		switch {
		case err != nil || math.IsNaN(value) || math.IsInf(value, 0):
			writeLine(a.opts.Streams.Err, "WARNING: Ignoring environment variable "+name+"="+strconv.Quote(raw)+": expected a number")
		case whole && value != math.Trunc(value):
			writeLine(a.opts.Streams.Err, "WARNING: Ignoring environment variable "+name+"="+strconv.Quote(raw)+": expected a whole number")
		case value < minimum:
			writeLine(a.opts.Streams.Err, "WARNING: Ignoring environment variable "+name+"="+strconv.Quote(raw)+": must be at least "+strconv.FormatFloat(minimum, 'g', -1, 64))
		default:
			return value, true
		}
		return 0, false
	}
	if value, ok := number("HTTP_TIMEOUT", 1, false); ok {
		settings.TimeoutSeconds = value
	}
	if value, ok := number("HTTP_RETRIES", 0, true); ok {
		settings.Retries = int(value)
	}
	if value, ok := number("JOIN_MAX_WORKERS", 1, true); ok {
		settings.JoinMaxWorkers = int(value)
	}
	if value, ok := number("PAGINATION_MAX_WORKERS", 1, true); ok {
		settings.PaginationMaxWorkers = int(value)
	}
}
