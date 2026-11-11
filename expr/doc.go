// Package expr holds the three small languages that API definitions and
// command lines are written in: templates, paths and conditions. Everything
// in it is a pure function of its arguments. Nothing here reads a file,
// sends a request or prints, and where a value comes from is up to the
// caller.
//
// Values are of the types [apikit.Decode] produces: nil, bool, string,
// [json.Number], []any and [*apikit.Object].
//
// # Templates
//
// A [Template] is text with placeholders: $name, or ${...} around any name.
// $$ is a literal $. The caller supplies the value of each name and says
// what happens to a placeholder that has none.
//
//	template := expr.ParseTemplate("orders/${order.id}/items?owner=$owner")
//	text, _ := template.Render(lookup, expr.RenderOptions{})
//	// orders/o-17/items?owner=ada, when lookup knows order.id and owner
//
// # Paths
//
// A [Path] selects a value inside a JSON value. It descends through objects
// field by field, selects an element of a list by the value of one of its
// keys, and collects a field of every element of a list.
//
//	path, err := expr.ParsePath("items[sku=leash].price")
//	price := path.Get(order, "")
//	// the price of the first item of the order whose sku is leash
//
// # Conditions
//
// A [Condition] is a path, an operator and a value to compare with.
// [ParseCondition] takes it apart, and [Holds] compares a value: as numbers,
// as times or as text, whichever both sides are.
//
//	condition, err := expr.ParseCondition("createdAt>=2026-01-31")
//	// condition.Path is "createdAt", condition.Op is expr.OpGreaterEqual
//	ok := expr.Holds("2026-02-03T09:30:00Z", condition.Op, condition.Value)
//	// true
package expr
