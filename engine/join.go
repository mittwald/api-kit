package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/expr"
)

// joinedID is the key of the ID of a related item in the object a join
// with several fields shows of it.
const joinedID = "id"

// A lookup is a join that one path of one column asks for one row.
type lookup struct {
	// row and slot say where the value goes: the row of the item, and the
	// place of the path among the paths of all columns.
	row, slot int
	relation
}

// A relation is a join at work for one item.
type relation struct {
	e *Engine
	// item is the item the join starts from.
	item *apikit.Object
	join *definition.Join
	// below is the path that is asked for below the name of the join:
	// "city" for a column "owner.city". It is empty for the join itself.
	below string
}

// joinFor returns the join that a path of a column asks for, and the path
// below its name. The join is nil for a path that names none.
func joinFor(resource *definition.Resource, path string) (*definition.Join, string) {
	for _, join := range resource.Joins {
		if rest, ok := below(path, join.Name); ok {
			return join, rest
		}
	}
	return nil, ""
}

// below reports whether path is name or leads on from it, and returns
// what follows name and its dot if so.
func below(path, name string) (string, bool) {
	if path == name {
		return "", true
	}
	return strings.CutPrefix(path, name+".")
}

// join runs the lookups, as many at the same time as the definition
// allows, and puts what they find into the rows, in place of what the
// items show themselves. It reports the rows that are complete: first
// those that wait for no lookup, then each row as its last one ends.
// Without lookups there is nothing to wait for and nothing to report. The
// first lookup that fails ends them all, and join returns what it failed
// with.
//
// A lookup makes its requests one after the other, those of its nested
// joins included. So a worker never waits for another one, and there are
// never more requests under way than workers.
func (e *Engine) join(ctx context.Context, lookups []lookup, rows [][]any) error {
	if len(lookups) == 0 {
		return nil
	}
	waiting := make([]int, len(rows))
	for _, wanted := range lookups {
		waiting[wanted.row]++
	}
	done := 0
	for _, count := range waiting {
		if count == 0 {
			done++
		}
	}
	report := func() {
		e.progress(Progress{Stage: StageJoin, Done: done, Total: len(rows)})
	}
	report()

	type outcome struct {
		lookup
		value any
		found bool
		err   error
	}
	// Leaving stops the lookups still under way and waits for them, in
	// this order.
	var workers sync.WaitGroup
	defer workers.Wait()
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	outcomes := make(chan outcome)
	var next atomic.Int64
	for range min(max(e.doc.HTTP.JoinMaxWorkers, 1), len(lookups)) {
		workers.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(lookups) {
					return
				}
				// What is remembered is answered even after the run has
				// ended; a lookup that is not begun then saves the work.
				got := outcome{lookup: lookups[i], err: run.Err()}
				if got.err == nil {
					got.value, got.found, got.err = got.find(run)
				}
				if got.err != nil {
					// One failure ends them all, and at once: no lookup
					// begins after it. The first one is the cause.
					cancel(got.err)
				}
				outcomes <- got
			}
		})
	}
	failed := false
	for range lookups {
		got := <-outcomes
		if failed = failed || got.err != nil; failed {
			continue
		}
		if got.found {
			rows[got.row][got.slot] = got.value
		}
		if waiting[got.row]--; waiting[got.row] == 0 {
			done++
			report()
		}
	}
	switch {
	case !failed:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	}
	return context.Cause(run)
}

// find fetches what the join shows for its item, and reports whether it
// shows anything. One related item shows as it is, unless the join is one
// to many. Several show as a list, and so do the items of a sub-resource,
// however many there are.
func (r relation) find(ctx context.Context) (any, bool, error) {
	if r.join.Exists {
		found, err := r.exists(ctx)
		return found, err == nil, err
	}
	return r.related(ctx)
}

// exists reports whether the item has a related item, whatever the join
// would show of it.
func (r relation) exists(ctx context.Context) (bool, error) {
	ids := r.ids()
	if len(ids) == 0 {
		return false, nil
	}
	e, join := r.e, r.join
	if join.By != "" {
		listing, err := e.relatedListing(ctx, join.Resource)
		if err != nil {
			return false, err
		}
		return slices.ContainsFunc(listOf(listing), func(item any) bool {
			object, ok := item.(*apikit.Object)
			return ok && slices.Contains(ids, apikit.Text(at(object, join.By)))
		}), nil
	}
	parent := e.doc.Resource(join.Resource).Parent
	for _, id := range ids {
		ref := Ref{Resource: join.Resource, ID: id}
		if parent != "" {
			ref = Ref{Resource: parent, ID: id, Sub: join.Resource}
		}
		answer, err := e.related(ctx, ref)
		if err != nil {
			return false, err
		}
		if list, ok := answer.([]any); ok && len(list) > 0 || !ok && !apikit.Falsy(answer) {
			return true, nil
		}
	}
	return false, nil
}

// related fetches the related items and returns what the join shows of
// them, and whether there are any.
func (r relation) related(ctx context.Context) (any, bool, error) {
	ids := r.ids()
	if len(ids) == 0 {
		return nil, false, nil
	}
	e, join := r.e, r.join
	if join.By != "" {
		return r.matched(ctx, ids)
	}
	// The items of a sub-resource are listed for their parent, whose ID
	// the item then holds.
	parent := e.doc.Resource(join.Resource).Parent
	values := []any{}
	for _, id := range ids {
		ref := Ref{Resource: join.Resource, ID: id}
		if parent != "" {
			ref = Ref{Resource: parent, ID: id, Sub: join.Resource}
		}
		answer, err := e.related(ctx, ref)
		if err != nil {
			return nil, false, err
		}
		if apikit.Falsy(answer) {
			continue
		}
		found := []any{answer}
		if list, ok := answer.([]any); ok && parent != "" {
			found = list
		}
		for _, related := range found {
			relatedID := id
			if parent != "" {
				object, _ := related.(*apikit.Object)
				relatedID, _ = e.IDOf(join.Resource, object)
			}
			value, err := r.shown(ctx, relatedID, related)
			if err != nil {
				return nil, false, err
			}
			if value != nil {
				values = append(values, value)
			}
		}
	}
	switch {
	case parent != "":
	case len(values) == 1 && len(ids) == 1 && !join.Many:
		return values[0], true, nil
	case len(values) == 0:
		return nil, false, nil
	}
	sortJoined(values, join)
	return values, true, nil
}

// matched returns what the join shows of the items of its resource's
// listing whose field By holds one of values.
func (r relation) matched(ctx context.Context, values []string) (any, bool, error) {
	e, join := r.e, r.join
	listing, err := e.relatedListing(ctx, join.Resource)
	if err != nil {
		return nil, false, err
	}
	var shown []any
	for _, item := range listOf(listing) {
		object, ok := item.(*apikit.Object)
		if !ok || !slices.Contains(values, apikit.Text(at(object, join.By))) {
			continue
		}
		id, _ := e.IDOf(join.Resource, object)
		value, err := r.shown(ctx, id, object)
		if err != nil {
			return nil, false, err
		}
		if value != nil {
			shown = append(shown, value)
		}
	}
	switch {
	case len(shown) == 0:
		return nil, false, nil
	case len(shown) == 1 && !join.Many:
		return shown[0], true, nil
	}
	sortJoined(shown, join)
	return shown, true, nil
}

// ids returns the IDs of the items the join relates its item to: what the
// column of the join leads to, or the ID of the first relation entry of
// the item that meets the conditions of the join.
func (r relation) ids() []string {
	join := r.join
	if len(join.Conditions) > 0 {
		fields := join.RelationFields
		if len(fields) == 0 {
			fields = []string{join.Column}
		}
		for _, field := range fields {
			value, _ := r.item.Get(field)
			entries, ok := value.([]any)
			if !ok {
				entries = []any{value}
			}
			for _, entry := range entries {
				object, ok := entry.(*apikit.Object)
				if !ok || !meets(object, join.Conditions) {
					continue
				}
				value, _ := object.Get(r.e.doc.Columns.ID)
				if id := joinID(value); id != "" {
					return []string{id}
				}
				return nil
			}
		}
		return nil
	}
	value := at(r.item, join.Column)
	list, ok := value.([]any)
	if !ok {
		list = []any{value}
	}
	var ids []string
	for _, element := range list {
		if id := joinID(element); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// meets reports whether a relation entry has each of the given values,
// compared as [apikit.Text] shows them.
func meets(entry *apikit.Object, conditions []definition.Pair) bool {
	for _, condition := range conditions {
		if value, _ := entry.Get(condition.Key); apikit.Text(value) != condition.Value {
			return false
		}
	}
	return true
}

// joinID returns a value as the ID of a related item: a text or a number
// that says something, and "" for anything else. An empty ID must never
// be asked for: it names the whole listing of a resource.
func joinID(value any) string {
	switch value.(type) {
	case string, json.Number:
		return idText(value)
	}
	return ""
}

// shown returns what the join shows of one related item, nil for nothing.
// id is the ID of the related item.
func (r relation) shown(ctx context.Context, id string, related any) (any, error) {
	join := r.join
	if r.below == "" {
		value := r.e.mapped(join.Resource, related, join.Fields)
		if object, ok := value.(*apikit.Object); ok {
			for _, nested := range join.Nested {
				inner, err := r.nested(ctx, nested, id, related)
				if err != nil {
					return nil, err
				}
				if inner != nil {
					object.Set(nested.Name, inner)
				}
			}
		}
		return value, nil
	}
	// A path below the join leads into a nested join, or else into the
	// related item, whatever the join shows of it.
	for _, nested := range join.Nested {
		rest, ok := below(r.below, nested.Name)
		if !ok {
			continue
		}
		value, err := r.nested(ctx, nested, id, related)
		if err != nil {
			return nil, err
		}
		if rest != "" {
			value = at(value, rest)
		}
		if value != nil && value != "" {
			return value, nil
		}
		break
	}
	if r.e.omitted(r.below) {
		return "", nil
	}
	if value := at(related, r.below); value != nil {
		return value, nil
	}
	return "", nil
}

// mapped returns what a join shows of a related item: the value of its
// one field, or with several fields an object of the ID of the item and
// each field. The ID is taken from where the resource keeps it and goes
// by one name, so that a path such as "owner.id" works for every join.
// What is no object shows as its text where one value is wanted.
func (e *Engine) mapped(resource string, related any, fields []string) any {
	// A field the definition leaves out of everything shown is not joined.
	fields = slices.DeleteFunc(slices.Clone(fields), e.omitted)
	object, ok := related.(*apikit.Object)
	switch {
	case !ok && len(fields) > 1, ok && len(fields) == 0:
		return nil
	case !ok:
		return apikit.Text(related)
	}
	// Nothing is taken from a field the definition leaves out, the ID
	// included, and a field that is an object loses those fields too.
	visible, _ := e.Omitted(object).(*apikit.Object)
	if len(fields) == 1 {
		return at(visible, fields[0])
	}
	id, _ := e.idValue(resource, visible)
	shown := apikit.NewObject()
	shown.Set(joinedID, id)
	for _, field := range fields {
		shown.Set(field, at(visible, field))
	}
	return shown
}

// nested returns what a nested join shows for one related item of the
// join, nil for nothing. id is the ID of the related item.
func (r relation) nested(ctx context.Context, nested *definition.Join, id string, related any) (any, error) {
	e := r.e
	nestedID := r.nestedID(nested, id, related)
	if id == "" || nestedID == "" {
		return nil, nil
	}
	if e.doc.Resource(nested.Resource).Parent != r.join.Resource {
		answer, err := e.related(ctx, Ref{Resource: nested.Resource, ID: nestedID})
		if err != nil || apikit.Falsy(answer) {
			return nil, err
		}
		return e.mapped(nested.Resource, answer, nested.Fields), nil
	}
	// A sub-resource of the related resource is listed for the related
	// item, and the ID picks one of the list.
	answer, err := e.related(ctx, Ref{Resource: r.join.Resource, ID: id, Sub: nested.Resource})
	if err != nil {
		return nil, err
	}
	candidates, ok := answer.([]any)
	if !ok {
		candidates = []any{answer}
	}
	for _, candidate := range candidates {
		object, ok := candidate.(*apikit.Object)
		if !ok {
			continue
		}
		if found, _ := e.IDOf(nested.Resource, object); found == nestedID {
			return e.mapped(nested.Resource, object, nested.Fields), nil
		}
	}
	return nil, nil
}

// nestedID returns the ID a nested join starts from, for the related
// item with the given ID. The item the join started from knows best: it
// may pair the two IDs in the entries of a list, or hold one of each.
// Otherwise the related item names the ID itself.
func (r relation) nestedID(nested *definition.Join, id string, related any) string {
	// A relation that conditions pick has no path that leads to its ID,
	// so the item has nothing to pair it with.
	if len(r.join.Conditions) == 0 {
		if paired := pairedID(r.item, r.join.Column, nested.Column, id); paired != "" {
			return paired
		}
		if joinID(at(r.item, r.join.Column)) == id {
			if single := joinID(at(r.item, nested.Column)); single != "" {
				return single
			}
		}
	}
	return joinID(at(related, nested.Column))
}

// pairedID returns the ID that an item pairs with the given one in the
// entries of a list. With the IDs at "meals.foodId" and their partners at
// "meals.recipe.chosen", each meal names a food and the recipe that goes
// with it. The result is "" when the two paths share no list, or no
// entry holds the ID.
func pairedID(item *apikit.Object, path, partnerPath, id string) string {
	steps, partnerSteps := strings.Split(path, "."), strings.Split(partnerPath, ".")
	shared := 0
	for shared < len(steps) && shared < len(partnerSteps) && steps[shared] == partnerSteps[shared] {
		shared++
	}
	if shared == 0 || shared == len(steps) || shared == len(partnerSteps) {
		return ""
	}
	entries, _ := at(item, strings.Join(steps[:shared], ".")).([]any)
	path, partnerPath = strings.Join(steps[shared:], "."), strings.Join(partnerSteps[shared:], ".")
	paired := ""
	for _, entry := range entries {
		if joinID(at(entry, path)) != id {
			continue
		}
		if partner := joinID(at(entry, partnerPath)); partner != "" {
			paired = partner
		}
	}
	return paired
}

// related fetches what a join asks the API for. An answer that is no
// success, and an ID that names no item, yield nothing and no error: a
// related item that cannot be had leaves its cell empty and does not
// spoil the result. Anything else, such as a credential that is missing
// or an API that cannot be reached, would fail every other request as
// well and is returned.
func (e *Engine) related(ctx context.Context, ref Ref) (any, error) {
	value, err := e.fetch(ctx, ref, nil)
	if err == nil {
		return value, nil
	}
	if _, refused := errors.AsType[*HTTPError](err); !refused && apikit.KindOf(err) != apikit.KindNotFound {
		return nil, err
	}
	e.log.LogAttrs(ctx, slog.LevelDebug, "join",
		slog.String("resource", ref.Resource), slog.String("id", ref.ID), slog.String("sub", ref.Sub),
		slog.String("error", err.Error()))
	return nil, nil
}

// omitted reports whether a path leads through a field the definition
// leaves out of everything shown.
func (e *Engine) omitted(path string) bool {
	if len(e.doc.Output.Omit) == 0 {
		return false
	}
	return slices.ContainsFunc(strings.Split(path, "."), func(part string) bool {
		// A segment may index or filter a list, as in tokens[0].
		name, _, _ := strings.Cut(part, "[")
		return slices.Contains(e.doc.Output.Omit, strings.Trim(name, `"'`))
	})
}

// Omitted returns value without the fields the definition leaves out of
// everything shown, at any depth. Objects and lists are copied, since a
// cached answer may be shared.
func (e *Engine) Omitted(value any) any {
	fields := e.doc.Output.Omit
	if len(fields) == 0 {
		return value
	}
	switch value := value.(type) {
	case *apikit.Object:
		if value == nil {
			return value
		}
		trimmed := apikit.NewObject()
		for key, inner := range value.All() {
			if !slices.Contains(fields, key) {
				trimmed.Set(key, e.Omitted(inner))
			}
		}
		return trimmed
	case []any:
		trimmed := make([]any, len(value))
		for i, inner := range value {
			trimmed[i] = e.Omitted(inner)
		}
		return trimmed
	}
	return value
}

// relatedListing fetches every page of the listing a join matches items
// in by a field. A listing the API refuses yields nothing, as a related
// item does.
func (e *Engine) relatedListing(ctx context.Context, resource string) (any, error) {
	value, err := e.fetchAll(ctx, Ref{Resource: resource}, nil, e.doc.Pagination.PageSize)
	if err == nil {
		return value, nil
	}
	if _, refused := errors.AsType[*HTTPError](err); !refused && apikit.KindOf(err) != apikit.KindNotFound {
		return nil, err
	}
	return nil, nil
}

// sortJoined puts the values of a join into the order the join asks for.
// A value that is no object is sorted by itself where the join shows the
// field that the order names.
func sortJoined(values []any, join *definition.Join) {
	key := func(value any, column string) any {
		if object, ok := value.(*apikit.Object); ok {
			return at(object, column)
		}
		if len(join.Fields) == 1 && join.Fields[0] == column {
			return value
		}
		return nil
	}
	sortValues(values, join.Sort, key)
}

// sortItems puts the items of a listing in order: by the first column, ties
// broken by the next ones.
func sortItems(values []any, orders []definition.Sort) {
	sortValues(values, orders, func(value any, column string) any {
		if object, ok := value.(*apikit.Object); ok {
			return at(object, column)
		}
		return nil
	})
}

// sortValues orders values stably by the columns key reads from them.
func sortValues(values []any, orders []definition.Sort, key func(value any, column string) any) {
	slices.SortStableFunc(values, func(a, b any) int {
		for _, order := range orders {
			result := compareSortValues(key(a, order.Column), key(b, order.Column))
			if order.Descending {
				result = -result
			}
			if result != 0 {
				return result
			}
		}
		return 0
	})
}

// compareSortValues returns -1, 0 or 1 for a before, equal to or after b.
// Numbers are compared by value, with false as 0 and true as 1. Anything
// else is compared as text the way a person reads it, see
// [compareNatural]. Nothing comes after everything.
func compareSortValues(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	a, b = truthAsNumber(a), truthAsNumber(b)
	x, isNumber := a.(json.Number)
	y, areNumbers := b.(json.Number)
	if isNumber && areNumbers {
		// Conditions compare numbers exactly, whatever their size.
		switch {
		case expr.Holds(x, expr.OpLess, y.String()):
			return -1
		case expr.Holds(x, expr.OpGreater, y.String()):
			return 1
		}
		return 0
	}
	return compareNatural(apikit.Text(a), apikit.Text(b))
}

// truthAsNumber returns a boolean as the number 0 or 1, and any other
// value as it is.
func truthAsNumber(value any) any {
	switch value := value.(type) {
	case bool:
		if value {
			return json.Number("1")
		}
		return json.Number("0")
	}
	return value
}

// compareNatural compares two texts the way a person reads them: a run
// of digits counts as the number it spells, so "1.9" comes before
// "1.10", and letters compare without regard to case. Where one text
// has digits and the other has not, the digits come first, and a text
// comes before every longer one that begins with it.
func compareNatural(a, b string) int {
	for a != "" && b != "" {
		var x, y string
		var xDigits, yDigits bool
		x, a, xDigits = cutRun(a)
		y, b, yDigits = cutRun(b)
		result := 0
		switch {
		case xDigits != yDigits:
			if xDigits {
				return -1
			}
			return 1
		case xDigits:
			// Without leading zeros the longer number is the greater one.
			x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if result = cmp.Compare(len(x), len(y)); result == 0 {
				result = strings.Compare(x, y)
			}
		default:
			result = strings.Compare(strings.ToLower(x), strings.ToLower(y))
		}
		if result != 0 {
			return result
		}
	}
	return cmp.Compare(len(a), len(b))
}

// cutRun cuts the run of digits, or of other characters, that text
// starts with. text must not be empty.
func cutRun(text string) (run, rest string, digits bool) {
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	digits = isDigit(text[0])
	end := 1
	for end < len(text) && isDigit(text[end]) == digits {
		end++
	}
	return text[:end], text[end:], digits
}
