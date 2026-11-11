package expr_test

import (
	"strconv"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/expr"
)

const order = `{
	"id": "o-17",
	"customer": {"name": "Ada", "address": {"city": "Springfield"}},
	"items": [
		{"sku": "leash", "quantity": 2, "gift": true, "price": 9.5},
		{"sku": "bowl", "quantity": 1, "gift": false, "maker": {"name": "Acme"}},
		"loose",
		null,
		[{"sku": "nested"}]
	],
	"pet": {"name": "Rex", "kind": "dog", "toys": [{"name": "ball", "size": 1.2}, {"name": "rope", "size": 3}]},
	"shipments": [
		{"parcels": [{"code": "a", "weight": 1}, {"code": "b", "weight": 2}]},
		{"parcels": [{"code": "b", "weight": 5}]},
		{"parcels": []}
	],
	"note": "",
	"count": 3,
	"paid": false,
	"voucher": null,
	"empty": {},
	"none": [],
	"extra": {
		"shop": "s-9",
		"owner": {"id": "u-1", "name": "Grace"},
		"id": "hidden",
		"voucher": "hidden",
		"badges": [{"name": "silver", "level": 2}, {"name": "gold", "level": 3}]
	}
}`

func TestPathGet(t *testing.T) {
	tests := []struct {
		path string
		want string // JSON
	}{
		// Dotted descent through objects.
		{"id", `"o-17"`},
		{"customer.name", `"Ada"`},
		{"customer.address.city", `"Springfield"`},
		{"customer.address", `{"city":"Springfield"}`},
		{"missing", `null`},
		{"customer.missing", `null`},
		{"customer.missing.city", `null`},
		{"voucher.code", `null`},
		{"empty.name", `null`},

		// A filter selects the first matching object of a list.
		{"items[sku=bowl].maker.name", `"Acme"`},
		{"items[sku=leash].quantity", `2`},
		{"items[sku=leash]", `{"sku":"leash","quantity":2,"gift":true,"price":9.5}`},
		{"extra.badges[name=gold].level", `3`},
		// It compares what a value reads as, so numbers and booleans match.
		{"items[quantity=1].sku", `"bowl"`},
		{"items[gift=true].sku", `"leash"`},
		{"items[price=9.5].sku", `"leash"`},
		// An empty value matches a key that is empty, null or absent.
		{"items[maker=].sku", `"leash"`},
		// A dot between the brackets does not split the path.
		{"pet.toys[size=1.2].name", `"ball"`},
		// Without a match there is nothing, not the whole list.
		{"items[sku=collar]", `null`},
		{"items[sku=collar].quantity", `null`},
		{"none[sku=leash]", `null`},
		{"missing[sku=leash]", `null`},

		// A filter on an object keeps the object when it matches.
		{"pet[kind=dog].name", `"Rex"`},
		{"pet[kind=dog].toys[name=rope].size", `3`},
		{"pet[kind=cat].name", `null`},
		{"pet[kind=cat]", `null`},
		// A filter on anything else finds nothing.
		{"id[kind=dog]", `null`},
		{"voucher[kind=dog]", `null`},
		{"customer.name[kind=dog]", `null`},

		// A segment applied to a list is applied to its objects.
		{"items.sku", `["leash","bowl",null,null,null]`},
		{"items.maker.name", `[null,"Acme",null,null,null]`},
		{"pet.toys.name", `["ball","rope"]`},
		{"none.sku", `[]`},
		{"shipments.parcels[code=b].weight", `[2,5,null]`},
		{"shipments.parcels.code", `[null,null,null]`},

		// A scalar in the middle of a path yields nothing.
		{"id.length", `null`},
		{"count.value", `null`},
		{"note.value", `null`},
		{"paid.value", `null`},
		{"customer.name.first", `null`},

		// The first field is also looked for in the object under "extra".
		{"shop", `"s-9"`},
		{"owner.name", `"Grace"`},
		{"badges[name=gold].level", `3`},
		{"badges.name", `["silver","gold"]`},
		{"extra.shop", `"s-9"`},
		// A field of the item itself wins, even when it is null.
		{"id", `"o-17"`},
		{"voucher", `null`},
		// Later segments are not looked for there.
		{"customer.shop", `null`},
	}
	item := decode(t, order)
	for _, test := range tests {
		path, err := expr.ParsePath(test.path)
		if err != nil {
			t.Errorf("ParsePath(%q): %v", test.path, err)
			continue
		}
		if got := path.Get(item, "extra"); !apikit.Equal(got, decode(t, test.want)) {
			t.Errorf("Get(%q) = %s, want %s", test.path, apikit.Text(got), test.want)
		}
	}
}

func TestPathGetExtraKey(t *testing.T) {
	tests := []struct {
		item     string
		path     string
		extraKey string
		want     string
	}{
		{order, "shop", "extra", `"s-9"`},
		{order, "shop", "", `null`},
		{order, "owner.name", "", `null`},
		{order, "badges[name=gold].level", "", `null`},
		{order, "name", "customer", `"Ada"`},
		{order, "name", "pet", `"Rex"`},
		// Only an object under the extra key is looked into.
		{`{"extra": "shop"}`, "shop", "extra", `null`},
		{`{"extra": [{"shop": "s-9"}]}`, "shop", "extra", `null`},
		{`{"extra": null}`, "shop", "extra", `null`},
		{`{"extra": {"other": 1}}`, "shop", "extra", `null`},
		// And only from an object.
		{`[{"extra": {"shop": "s-9"}}]`, "shop", "extra", `[null]`},
	}
	for _, test := range tests {
		path, err := expr.ParsePath(test.path)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", test.path, err)
		}
		if got := path.Get(decode(t, test.item), test.extraKey); !apikit.Equal(got, decode(t, test.want)) {
			t.Errorf("Get(%q) with extra key %q = %s, want %s", test.path, test.extraKey, apikit.Text(got), test.want)
		}
	}
}

func TestPathGetOnValuesThatAreNoObjects(t *testing.T) {
	tests := []struct {
		item string
		path string
		want string
	}{
		{`[{"name": "ball"}, "rope", null, 7, [{"name": "nested"}]]`, "name", `["ball",null,null,null,null]`},
		{`[{"tags": [{"k": "a", "v": 1}]}, {"tags": []}, {}]`, "tags[k=a].v", `[1,null,null]`},
		{`[]`, "name", `[]`},
		{`"text"`, "name", `null`},
		{`7`, "name", `null`},
		{`true`, "name", `null`},
		{`null`, "name", `null`},
		{`null`, "name[k=v].x", `null`},
	}
	for _, test := range tests {
		path, err := expr.ParsePath(test.path)
		if err != nil {
			t.Fatalf("ParsePath(%q): %v", test.path, err)
		}
		if got := path.Get(decode(t, test.item), "extra"); !apikit.Equal(got, decode(t, test.want)) {
			t.Errorf("Get(%s, %q) = %s, want %s", test.item, test.path, apikit.Text(got), test.want)
		}
	}
}

func TestPathGetReturnsTheValueItself(t *testing.T) {
	item := decode(t, order).(*apikit.Object)
	customer, _ := item.Get("customer")
	path, err := expr.ParsePath("customer")
	if err != nil {
		t.Fatal(err)
	}
	if got := path.Get(item, ""); got != customer {
		t.Error("Get returned a copy of the object")
	}

	var zero expr.Path
	if got := zero.Get(item, "extra"); got != any(item) {
		t.Errorf("the zero Path selects %s, want the item", apikit.Text(got))
	}
	if zero.String() != "" {
		t.Errorf("the zero Path reads %q", zero.String())
	}
}

func TestParsePath(t *testing.T) {
	valid := []string{
		"a",
		"a.b.c",
		"a b.c-d._e",
		"a[k=v]",
		"a[k=v].b",
		"a.b[k=v].c[l=w]",
		"a[k=].b",
		"a[k=v=w].b",
		"a[k=1.2].b",
		"a[k=x y].b",
		"a[ k = v ]",
	}
	for _, text := range valid {
		path, err := expr.ParsePath(text)
		if err != nil {
			t.Errorf("ParsePath(%q): %v", text, err)
			continue
		}
		if path.String() != text {
			t.Errorf("ParsePath(%q).String() = %q", text, path.String())
		}
	}

	invalid := []struct {
		path    string
		problem string // part of the message
	}{
		{"", "empty path"},
		{".", "empty segment"},
		{"a.", "empty segment"},
		{".a", "empty segment"},
		{"a..b", "empty segment"},
		{"a[k=v].", "empty segment"},
		{"a[", "[ without ]"},
		{"a[k=v", "[ without ]"},
		{"a[k=v.b", "[ without ]"},
		{"a]", "] without ["},
		{"a]b", "] without ["},
		{"a[k=v]]", "] without ["},
		{"a.b]", "] without ["},
		{"a[k]", "has no ="},
		{"a[]", "has no ="},
		{"a[=v]", "has no key"},
		{"a[=]", "has no key"},
		{"[k=v]", "without a field"},
		{"a.[k=v]", "without a field"},
		{"a[k=v]b", "expected a dot"},
		{"a[k=v][l=w]", "expected a dot"},
	}
	for _, test := range invalid {
		path, err := expr.ParsePath(test.path)
		if err == nil {
			t.Errorf("ParsePath(%q) = %v, want an error", test.path, path)
			continue
		}
		if !strings.Contains(err.Error(), test.problem) {
			t.Errorf("ParsePath(%q): %v, want a message with %q", test.path, err, test.problem)
		}
		if test.path != "" && !strings.Contains(err.Error(), strconv.Quote(test.path)) {
			t.Errorf("ParsePath(%q): %v, want a message that names the path", test.path, err)
		}
	}
}
