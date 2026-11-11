package cli

import (
	"cmp"
	"encoding/json"
	"math/big"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mittwald/cli-kit/term"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/engine"
	"github.com/mittwald/api-kit/output"
)

// view says how a result is shown.
type view struct {
	spec     output.Spec
	sortBy   string
	sortDesc bool
}

// render writes a result to the output stream in the requested format. An
// empty result writes nothing.
func (a *App) render(cmd *cobra.Command, session *session, result *engine.Result, v view) error {
	if apikit.Falsy(result.Value) {
		return nil
	}
	items := result.Items()
	if v.sortBy != "" {
		if _, isList := result.Value.([]any); isList {
			sorted, err := sortItems(items, v.sortBy, v.sortDesc)
			if err != nil {
				return err
			}
			items = sorted
		}
	}
	if v.spec.Deprecated != "" {
		a.deprecated("output format "+v.spec.Deprecated, "-o "+string(v.spec.Format))
	}

	w := cmd.OutOrStdout()
	format := v.spec.Format
	// What the API returned needs nothing more.
	switch format {
	case output.JSONRaw:
		return failed(output.WriteJSON(w, items))
	case output.ID:
		return failed(output.WriteIDs(w, items, func(item *apikit.Object) (string, bool) {
			return session.engine.IDOf(result.Resource, item)
		}))
	case output.Name:
		return failed(output.WriteNames(w, items, session.doc.Output.NameFields))
	}

	joins := !a.globals.noJoins
	if format.Tabular() {
		links, err := a.globals.linked(w)
		if err != nil {
			return err
		}
		human := format == output.Text || format == output.Wide
		projected, err := session.engine.Project(cmd.Context(), result.Resource, items, engine.ProjectOptions{
			AllColumns: format == output.Wide,
			Joins:      joins,
			Links:      links && human,
		})
		if err != nil {
			return failed(err)
		}
		table := output.Table{Rows: projected.Rows, Links: projected.Links}
		for _, column := range projected.Columns {
			table.Headers = append(table.Headers, column.Label)
		}
		return failed(output.WriteTable(w, table, output.TableOptions{
			Format:        format,
			Color:         term.Colored(w),
			Links:         links,
			Ellipsis:      !a.globals.noEllipsis,
			MaxCharacters: session.doc.Output.MaxCharacters,
			KeepWhole:     session.doc.Output.KeepWhole,
			Colors:        session.doc.Resource(result.Resource).Colors,
			Palette:       output.NewPalette(a.opts.Getenv, a.opts.EnvPrefix),
			Getenv:        a.opts.Getenv,
		}))
	}

	// The other formats show the items themselves, completed with what the
	// joins found and with their links. Joins of many related items have
	// room here.
	projected, err := session.engine.Project(cmd.Context(), result.Resource, items, engine.ProjectOptions{
		AllColumns: true,
		Joins:      joins,
		JoinMany:   joins && format != output.Link,
		Links:      true,
	})
	if err != nil {
		return failed(err)
	}
	switch format {
	case output.JSON:
		return failed(output.WriteJSON(w, items))
	case output.YAML:
		return failed(output.WriteYAML(w, items))
	case output.JSONPath:
		return failed(output.WriteJSONPath(w, items, v.spec.Argument))
	case output.Columns:
		return failed(output.WriteColumns(w, items, v.spec.Argument))
	case output.Link:
		return failed(output.WriteLinks(w, projected.Links))
	}
	return apikit.Errorf(apikit.KindUsage, "output format %s is not available for this command", format)
}

// sortItems orders items by one of their fields. The field is named
// without regard to case, as it is typed on a command line, and must be a
// field of the first item.
func sortItems(items []any, field string, descending bool) ([]any, error) {
	first, _ := items[0].(*apikit.Object)
	index := slices.IndexFunc(first.Keys(), func(key string) bool { return strings.EqualFold(key, field) })
	if index < 0 {
		return nil, apikit.Errorf(apikit.KindUsage, "cannot sort by %q: the items have no such field", field)
	}
	key := first.Keys()[index]
	value := func(item any) any {
		object, _ := item.(*apikit.Object)
		found, _ := object.Get(key)
		return found
	}
	sorted := slices.Clone(items)
	slices.SortStableFunc(sorted, func(a, b any) int {
		order := compareValues(value(a), value(b))
		if descending {
			return -order
		}
		return order
	})
	return sorted, nil
}

// compareValues orders two values of an item: numbers by value, everything
// else by its text.
func compareValues(a, b any) int {
	if x, ok := a.(json.Number); ok {
		if y, ok := b.(json.Number); ok {
			left, _, errLeft := big.ParseFloat(x.String(), 10, 512, big.ToNearestEven)
			right, _, errRight := big.ParseFloat(y.String(), 10, 512, big.ToNearestEven)
			if errLeft == nil && errRight == nil {
				return left.Cmp(right)
			}
		}
	}
	return cmp.Compare(apikit.Text(a), apikit.Text(b))
}

// deprecated tells the user, once per run and on the error stream, that
// something still works but has a replacement.
func (a *App) deprecated(what, replacement string) {
	if a.told[what] {
		return
	}
	a.told[what] = true
	writeLine(a.opts.Streams.Err, "WARNING: "+what+" is deprecated, use "+replacement)
}
