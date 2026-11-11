package expr_test

import (
	"encoding/json"
	"fmt"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/expr"
)

func ExampleTemplate_Render() {
	values := map[string]any{
		"customer": "Ada",
		"order.id": "o-17",
		"total":    json.Number("12.50"),
	}
	lookup := func(name string) (any, bool) {
		value, ok := values[name]
		return value, ok
	}

	template := expr.ParseTemplate("Hello $customer, order ${order.id} costs $$${total} (${note})")
	text, _ := template.Render(lookup, expr.RenderOptions{})
	fmt.Println(text)

	text, missing := template.Render(lookup, expr.RenderOptions{Missing: expr.Report})
	fmt.Println(text)
	for _, placeholder := range missing {
		fmt.Println("no value for", placeholder.Raw)
	}
	// Output:
	// Hello Ada, order o-17 costs $12.50 (${note})
	// Hello Ada, order o-17 costs $12.50 ()
	// no value for ${note}
}

func ExampleTemplate_Sole() {
	for _, text := range []string{"${gift}", "gift: ${gift}", "$gift"} {
		name, ok := expr.ParseTemplate(text).Sole()
		fmt.Printf("%q %q %v\n", text, name, ok)
	}
	// Output:
	// "${gift}" "gift" true
	// "gift: ${gift}" "" false
	// "$gift" "" false
}

func ExamplePath_Get() {
	order, err := apikit.Unmarshal([]byte(`{
		"customer": {"name": "Ada"},
		"items": [
			{"sku": "leash", "price": 9.5},
			{"sku": "bowl", "price": 4}
		],
		"computed": {"total": 13.5}
	}`))
	if err != nil {
		panic(err)
	}

	for _, text := range []string{"customer.name", "items[sku=bowl].price", "items.sku", "total", "items[sku=collar].price"} {
		path, err := expr.ParsePath(text)
		if err != nil {
			panic(err)
		}
		// A field the order lacks is looked for under "computed".
		value, err := apikit.Marshal(path.Get(order, "computed"))
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s: %s\n", path, value)
	}
	// Output:
	// customer.name: "Ada"
	// items[sku=bowl].price: 4
	// items.sku: ["leash","bowl"]
	// total: 13.5
	// items[sku=collar].price: null
}

func ExampleParseCondition() {
	order, err := apikit.Unmarshal([]byte(`{
		"status": "shipped",
		"shippedAt": "2026-02-03T09:30:00+01:00",
		"items": [{"sku": "leash", "quantity": 2}]
	}`))
	if err != nil {
		panic(err)
	}

	for _, text := range []string{"status!=open", "shippedAt>=2026-01-31", "items[sku=leash].quantity>2", "status"} {
		condition, err := expr.ParseCondition(text)
		if err != nil {
			fmt.Println(err)
			continue
		}
		path, err := expr.ParsePath(condition.Path)
		if err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Printf("%s: %v\n", condition, expr.Holds(path.Get(order, ""), condition.Op, condition.Value))
	}
	// Output:
	// status!=open: true
	// shippedAt>=2026-01-31: true
	// items[sku=leash].quantity>2: false
	// "status" is not a condition: expected a path, one of the operators = != < <= > >= and a value, like enabled=false, status!=closed or createdAt>=2026-01-31
}

func ExampleHolds() {
	fmt.Println(expr.Holds(json.Number("2"), expr.OpEqual, "2.0"))
	fmt.Println(expr.Holds("2026-03-09T15:00:00Z", expr.OpEqual, "2026-03-09T16:00:00+01:00"))
	fmt.Println(expr.Holds("01234", expr.OpEqual, "1234"))
	fmt.Println(expr.Holds(nil, expr.OpLess, "2026-03-09"))
	// Output:
	// true
	// true
	// false
	// false
}
