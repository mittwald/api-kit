package engine_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// projecting returns an engine for the pet shop with an overlay of the
// test's own, and the fake API it talks to. change may adjust the
// definition and the options before the engine is made.
func projecting(t *testing.T, overlay string, change func(doc *definition.Document, o *engine.Options)) *shop {
	t.Helper()
	doc := load(t, overlay)
	api := apitest.New(t)
	waits := &pauses{}
	options := engine.Options{Definition: doc, HTTP: api.Client(), Sleep: waits.sleep}
	if change != nil {
		change(doc, &options)
	}
	made, err := engine.New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &shop{Engine: made, doc: doc, api: api, pauses: waits}
}

// itemsOf parses a JSON list of items.
func itemsOf(t *testing.T, list string) []any {
	t.Helper()
	parsed, ok := decode(t, list).([]any)
	if !ok {
		t.Fatalf("%s is no list", list)
	}
	return parsed
}

// columnsOf returns the columns of a table, each as its label, followed by
// its paths where they are not just the label.
func columnsOf(table *engine.Table) []string {
	var columns []string
	for _, column := range table.Columns {
		text := column.Label
		if !slices.Equal(column.Paths, []string{column.Label}) {
			text += "=" + strings.Join(column.Paths, "+")
		}
		columns = append(columns, text)
	}
	return columns
}

func ExampleEngine_Project() {
	// Pets show the name of their owner, who is an item of another
	// resource.
	const joins = `
resources:
  pets:
    columns: [owner]
    joins:
      - {name: owner, column: ownerId, resource: owners, join: name}
`
	doc, _, err := definition.Load(
		definition.Source{Name: "petshop.yaml", Data: []byte(apitest.PetShop)},
		definition.LoadOptions{Overlays: []definition.Source{{Name: "joins.yaml", Data: []byte(joins)}}})
	if err != nil {
		panic(err)
	}
	// A stand-in for the API that knows one owner.
	api := doerFunc(func(req *http.Request) (*http.Response, error) {
		fmt.Println(req.Method, req.URL)
		const ada = `{"id":"0b0b0b0b","name":"Ada","city":"Bern"}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(ada))}, nil
	})
	shop, err := engine.New(engine.Options{Definition: doc, HTTP: api})
	if err != nil {
		panic(err)
	}
	pets, err := apikit.Unmarshal([]byte(`[{"tag":"REX-7","name":"Rex","species":"dog","ownerId":"0b0b0b0b"}]`))
	if err != nil {
		panic(err)
	}

	table, err := shop.Project(context.Background(), "pets", pets.([]any), engine.ProjectOptions{Joins: true})
	if err != nil {
		panic(err)
	}
	fmt.Println(strings.Join(columnsOf(table), " "))
	for _, row := range table.Rows {
		fmt.Println(row...)
	}
	// The pet has been given the name of its owner as well.
	fmt.Println(apikit.Text(pets))
	// Output:
	// GET https://api.example.test/v1/owners/0b0b0b0b
	// tag name species ownerId owner
	// REX-7 Rex dog 0b0b0b0b Ada
	// [{"tag": "REX-7", "name": "Rex", "species": "dog", "ownerId": "0b0b0b0b", "extra": {"owner": "Ada"}}]
}

func TestProjectColumns(t *testing.T) {
	const (
		// Toys have two columns and two joins, balls have neither.
		overlay = `
output:
  columnOrder: [id, "${singular}Id", tag, number, name, title, description]
resources:
  toys:
    columns:
      - tag
      - {label: name, paths: [title, maker.name]}
    joins:
      - {name: maker, column: makerId, resource: makers, join: name}
      - {name: parts, column: partIds, resource: parts, join: name, many: true}
`
		toys = `[
			{"species":"dog","name":"Bone","toyId":"t","id":"1","weight":4.5,"age":3,"squeaks":true,"note":null,
			 "sizes":["s"],"maker":{"name":"Ada"},"Title":"T","description":"d","tag":"T-1","number":7,"nickname":"b","shelfId":"s"},
			{"species":"cat","color":"red","id":"2"}]`
	)
	all := func(joins, many bool) engine.ProjectOptions {
		return engine.ProjectOptions{AllColumns: true, Joins: joins, JoinMany: many}
	}
	tests := map[string]struct {
		overlay  string
		resource string
		options  engine.ProjectOptions
		want     []string
	}{
		"the columns of the definition": {
			resource: "toys", options: engine.ProjectOptions{Joins: true, JoinMany: true},
			want: []string{"tag", "name=title+maker.name"},
		},
		// The fields the definition puts in front come first, in its
		// order and whatever their case, then what holds "name", then
		// what holds "id". The rest keeps its order: the joins, the
		// fields of the items that hold a text, a number or a boolean,
		// and the columns of the definition that are not among them.
		"all fields, the telling ones first": {
			resource: "toys", options: all(true, true),
			want: []string{
				"id", "toyId", "tag", "number", "name", "name=title+maker.name", "Title", "description",
				"nickname", "shelfId", "maker", "parts", "species", "weight", "age", "squeaks", "color",
			},
		},
		"a join to many is no column unless it is fetched": {
			resource: "toys", options: all(true, false),
			want: []string{
				"id", "toyId", "tag", "number", "name", "name=title+maker.name", "Title", "description",
				"nickname", "shelfId", "maker", "species", "weight", "age", "squeaks", "color",
			},
		},
		"without joins none is a column": {
			resource: "toys", options: all(false, false),
			want: []string{
				"id", "toyId", "tag", "number", "name", "name=title+maker.name", "Title", "description",
				"nickname", "shelfId", "species", "weight", "age", "squeaks", "color",
			},
		},
		"a resource without columns shows all fields": {
			resource: "balls", options: engine.ProjectOptions{},
			want: []string{
				"id", "tag", "number", "name", "Title", "description", "nickname", "toyId", "shelfId",
				"species", "weight", "age", "squeaks", "color",
			},
		},
		"the singular of the resource is the definition's": {
			overlay:  "output:\n  columnOrder: [\"${singular}Id\"]\nresources:\n  mice:\n    singular: mouse\n",
			resource: "mice", options: all(false, false),
			want: []string{"mouseId", "name", "nickname", "id", "shelfId", "species", "weight", "age", "squeaks", "Title", "description", "tag", "number", "color"},
		},
		"no order but the one in the code": {
			overlay:  "output:\n  columnOrder: []\n",
			resource: "balls", options: engine.ProjectOptions{},
			want: []string{"name", "nickname", "toyId", "id", "shelfId", "species", "weight", "age", "squeaks", "Title", "description", "tag", "number", "color"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if test.overlay == "" {
				test.overlay = overlay
			}
			list := toys
			if test.resource == "mice" {
				list = strings.Replace(toys, `"toyId"`, `"mouseId"`, 1)
			}
			shop := projecting(t, test.overlay, nil)
			table, err := shop.Project(t.Context(), test.resource, itemsOf(t, list), test.options)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			if got := columnsOf(table); !slices.Equal(got, test.want) {
				t.Errorf("columns:\n  %s\nwant:\n  %s", strings.Join(got, " "), strings.Join(test.want, " "))
			}
			for i, row := range table.Rows {
				if len(row) != len(table.Columns) {
					t.Errorf("row %d has %d values for %d columns", i, len(row), len(table.Columns))
				}
			}
		})
	}
}

func TestProjectValues(t *testing.T) {
	tests := map[string]struct {
		// column is a column of toys, as YAML.
		column string
		item   string
		want   string
	}{
		// A field shows as it is.
		"a text":            {`name`, `{"name":"Bone"}`, `"Bone"`},
		"a number":          {`price`, `{"price":2.50}`, `2.50`},
		"a boolean":         {`squeaks`, `{"squeaks":false}`, `false`},
		"null":              {`note`, `{"note":null}`, `null`},
		"a field not there": {`note`, `{"name":"Bone"}`, `""`},

		// A path shows what it leads to.
		"a path into an object":            {`maker.city`, `{"maker":{"city":"Bern"}}`, `"Bern"`},
		"a path to zero":                   {`stock.count`, `{"stock":{"count":0}}`, `0`},
		"a path to false":                  {`stock.low`, `{"stock":{"low":false}}`, `false`},
		"a path to null":                   {`stock.count`, `{"stock":{"count":null}}`, `""`},
		"a path to an empty text":          {`maker.city`, `{"maker":{"city":""}}`, `""`},
		"a path that leads nowhere":        {`maker.city`, `{"maker":"Ada"}`, `""`},
		"a path that is none":              {`maker..city`, `{"maker":{"city":"Bern"}}`, `""`},
		"a path with a filter":             {`prices[currency=EUR].amount`, `{"prices":[{"currency":"USD","amount":3},{"currency":"EUR","amount":2}]}`, `2`},
		"a filter alone":                   {`makers[role=lead]`, `{"makers":[{"role":"helper","name":"Bob"},{"role":"lead","name":"Ada"}]}`, `"Ada"`},
		"a path into a list":               {`visits.reason`, `{"visits":[{"reason":"checkup"},{"reason":"shot"}]}`, `"checkup, shot"`},
		"a path to an object":              {`maker.address`, `{"maker":{"address":{"street":"Main St","cityName":"Bern"}}}`, `"Bern"`},
		"a path into what was added":       {`owner.name`, `{"extra":{"owner":{"name":"Ada"}}}`, `"Ada"`},
		"a field named like the path":      {`maker.city`, `{"maker.city":"Basel"}`, `"Basel"`},
		"which the path comes before":      {`maker.city`, `{"maker.city":"Basel","maker":{"city":"Bern"}}`, `"Bern"`},
		"a field of what was added is not": {`owner`, `{"extra":{"owner":"Ada"}}`, `""`},

		// An object shows as its name.
		"the fields with name in their name":   {`maker`, `{"maker":{"id":"m","firstName":"Ada","city":"Bern","LASTNAME":"Lovelace"}}`, `"Ada Lovelace"`},
		"else those of the objects it holds":   {`team`, `{"team":{"lead":{"name":"Ada"},"size":2,"deputy":{"firstName":"Bob","lastName":"Byte"},"office":{"city":"Bern"}}}`, `"Ada, Bob Byte"`},
		"an object without a name":             {`team`, `{"team":{"size":2,"office":{"city":"Bern"}}}`, `""`},
		"an empty object":                      {`team`, `{"team":{}}`, `""`},
		"a name that is no text shows as text": {`maker`, `{"maker":{"name":7,"nickname":true}}`, `"7 true"`},

		// A list shows its elements.
		"a list of texts":                   {`tags`, `{"tags":["chew","fetch"]}`, `"chew, fetch"`},
		"an empty list":                     {`tags`, `{"tags":[]}`, `""`},
		"a list of objects by their name":   {`makers`, `{"makers":[{"id":1,"name":"A","displayName":"Ada"},{"name":"B","displayName":"Bob"}]}`, `"Ada, Bob"`},
		"an object of the list without it":  {`makers`, `{"makers":[{"name":"Ada"},{"id":2},"Bob"]}`, `"Ada, , "`},
		"a list of objects without a name":  {`makers`, `{"makers":[{"id":1},{"id":2}]}`, `""`},
		"a list of anything else":           {`sizes`, `{"sizes":[1,2,3]}`, `""`},
		"a list that starts with a text":    {`sizes`, `{"sizes":["s",2,null,true]}`, `"s, 2, , true"`},
		"a path to a list of objects":       {`shop.makers`, `{"shop":{"makers":[{"name":"Ada"},{"name":"Bob"}]}}`, `"Ada, Bob"`},
		"a path to a list of other things":  {`shop.sizes`, `{"shop":{"sizes":[1,2]}}`, `""`},
		"a path to an object without names": {`shop.address`, `{"shop":{"address":{"street":"Main St"}}}`, `""`},

		// Several paths show as one text: what each finds, once, in the
		// order of the paths.
		"several paths":                  {`{label: who, paths: [maker.name, owner, note, tag]}`, `{"maker":{"name":"Ada"},"owner":"Bob","note":null,"tag":"T-1"}`, `"Ada, Bob, T-1"`},
		"the same value once":            {`{label: who, paths: [maker.name, owner, lead]}`, `{"maker":{"name":"Ada"},"owner":"Bob","lead":"Ada"}`, `"Ada, Bob"`},
		"values that are no text":        {`{label: state, paths: [squeaks, stock, price, sold]}`, `{"squeaks":true,"stock":0,"price":2.50,"sold":false}`, `"true, 0, 2.50, false"`},
		"white space around a value":     {`{label: who, paths: [owner, lead]}`, `{"owner":"  Bob ","lead":"\t"}`, `"Bob"`},
		"paths that find nothing":        {`{label: who, paths: [owner, lead]}`, `{"name":"Bone"}`, `""`},
		"one path with a label is as is": {`{label: cost, paths: [price]}`, `{"price":2.50}`, `2.50`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := projecting(t, "resources:\n  toys:\n    columns:\n      - "+test.column+"\n", nil)
			table, err := shop.Project(t.Context(), "toys", itemsOf(t, "["+test.item+"]"), engine.ProjectOptions{Joins: true})
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			wantJSON(t, table.Rows, "[["+test.want+"]]")
		})
	}
}

func TestProjectCompletesItems(t *testing.T) {
	// The link names what a column adds to the item.
	const overlay = `
output:
  columnOrder: [title]
resources:
  toys:
    urlTemplate: {url: toys, query: {tag: "${tag}", maker: "${makerName}"}}
    columns:
      - tag
      - {label: makerName, paths: [maker.name]}
      - {label: name, paths: [title]}
      - {label: state, paths: [stock, sold]}
      - shelf.row
`
	linked := engine.ProjectOptions{Links: true}

	t.Run("with what the columns show and the link", func(t *testing.T) {
		shop := projecting(t, overlay, nil)
		toys := itemsOf(t, `[
			{"tag":"T-1","name":"Bone","title":"Squeaky","maker":{"name":"Ada"},"stock":3,"shelf":{"row":2}},
			{"tag":"T-2","maker":{"name":"Bob Byte"},"extra":{"seen":true,"makerName":"old"}}]`)
		table, err := shop.Project(t.Context(), "toys", toys, linked)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		wantJSON(t, table.Rows, `[["T-1","Ada","Squeaky","3",2],["T-2","Bob Byte","","",""]]`)
		const first, second = "https://shop.example.test/toys?tag=T-1&maker=Ada", "https://shop.example.test/toys?tag=T-2&maker=Bob%20Byte"
		if !slices.Equal(table.Links, []string{first, second}) {
			t.Errorf("links: %q", table.Links)
		}
		// What the item holds under the label of a column stays as it is,
		// whatever the column shows: the name of the first toy.
		wantJSON(t, toys, `[`+
			`{"tag":"T-1","name":"Bone","title":"Squeaky","maker":{"name":"Ada"},"stock":3,"shelf":{"row":2},`+
			`"extra":{"makerName":"Ada","state":"3","shelf.row":2,"link":"`+first+`"}},`+
			`{"tag":"T-2","maker":{"name":"Bob Byte"},`+
			`"extra":{"seen":true,"makerName":"Bob Byte","name":"","state":"","shelf.row":"","link":"`+second+`"}}]`)
	})

	t.Run("without the link unless it is asked for", func(t *testing.T) {
		shop := projecting(t, overlay, nil)
		toys := itemsOf(t, `[{"tag":"T-1","name":"Bone"}]`)
		table, err := shop.Project(t.Context(), "toys", toys, engine.ProjectOptions{})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if table.Links != nil {
			t.Errorf("links: %q, want none", table.Links)
		}
		wantJSON(t, toys, `[{"tag":"T-1","name":"Bone","extra":{"makerName":"","state":"","shelf.row":""}}]`)
	})

	t.Run("a resource without a link template has no links", func(t *testing.T) {
		shop := projecting(t, overlay, nil)
		pets := itemsOf(t, `[{"tag":"REX-7","name":"Rex","species":"dog","ownerId":"0b0b0b0b"}]`)
		table, err := shop.Project(t.Context(), "pets", pets, linked)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if table.Links != nil {
			t.Errorf("links: %q, want none", table.Links)
		}
		// Every column is a field of the item, so nothing is added.
		wantJSON(t, pets, `[{"tag":"REX-7","name":"Rex","species":"dog","ownerId":"0b0b0b0b"}]`)
	})

	t.Run("an item that uses the key itself keeps what it has there", func(t *testing.T) {
		shop := projecting(t, overlay, nil)
		toys := itemsOf(t, `[{"tag":"T-1","extra":"large"},{"tag":"T-2","extra":null}]`)
		table, err := shop.Project(t.Context(), "toys", toys, linked)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		wantJSON(t, table.Rows, `[["T-1","","","",""],["T-2","","","",""]]`)
		wantJSON(t, toys, `[{"tag":"T-1","extra":"large"},{"tag":"T-2","extra":null}]`)
		if want := []string{"https://shop.example.test/toys?tag=T-1", "https://shop.example.test/toys?tag=T-2"}; !slices.Equal(table.Links, want) {
			t.Errorf("links: %q, want %q", table.Links, want)
		}
	})

	t.Run("what is no object has no fields", func(t *testing.T) {
		shop := projecting(t, overlay, nil)
		toys := itemsOf(t, `["T-1",null,7,["T-2"]]`)
		table, err := shop.Project(t.Context(), "toys", toys, engine.ProjectOptions{Joins: true, JoinMany: true, Links: true})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		wantJSON(t, table.Rows, `[["","","","",""],["","","","",""],["","","","",""],["","","","",""]]`)
		if !slices.Equal(table.Links, []string{"", "", "", ""}) {
			t.Errorf("links: %q, want four empty ones", table.Links)
		}
		wantJSON(t, toys, `["T-1",null,7,["T-2"]]`)

		table, err = shop.Project(t.Context(), "toys", toys, engine.ProjectOptions{AllColumns: true})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if got, want := columnsOf(table), []string{"name=title", "makerName=maker.name", "tag", "state=stock+sold", "shelf.row"}; !slices.Equal(got, want) {
			t.Errorf("columns: %q, want %q", got, want)
		}
	})

	t.Run("no items make a table without rows", func(t *testing.T) {
		var reports []engine.Progress
		shop := projecting(t, overlay, func(_ *definition.Document, o *engine.Options) {
			o.Progress = func(p engine.Progress) { reports = append(reports, p) }
		})
		table, err := shop.Project(t.Context(), "toys", nil, engine.ProjectOptions{Joins: true, Links: true})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if len(table.Columns) != 5 || len(table.Rows) != 0 || table.Links != nil {
			t.Errorf("table: %+v", table)
		}
		// Nor is there progress to report where no column asks for a
		// join.
		if _, err = shop.Project(t.Context(), "toys", itemsOf(t, `[{"tag":"T-1"}]`), engine.ProjectOptions{Joins: true}); err != nil {
			t.Fatalf("Project: %v", err)
		}
		if len(reports) != 0 {
			t.Errorf("progress: %+v, want none", reports)
		}
	})
}
