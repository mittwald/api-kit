package engine_test

import (
	"fmt"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

func ExampleEngine_Link() {
	const links = `
resources:
  pets:
    urlTemplate:
      url: pets/open
      query: {pet: "${id}", title: "${name} (${tag})"}
`
	doc, _, err := definition.Load(
		definition.Source{Name: "petshop.yaml", Data: []byte(apitest.PetShop)},
		definition.LoadOptions{Overlays: []definition.Source{{Name: "links.yaml", Data: []byte(links)}}})
	if err != nil {
		panic(err)
	}
	shop, err := engine.New(engine.Options{Definition: doc, Environment: "staging"})
	if err != nil {
		panic(err)
	}
	pet, err := apikit.Unmarshal([]byte(`{"id":"0a1b2c3d","tag":"REX-7","name":"Rex"}`))
	if err != nil {
		panic(err)
	}
	fmt.Println(shop.Link("pets", pet.(*apikit.Object)))
	// Output:
	// https://shop.staging.example.test/pets/open?pet=0a1b2c3d&title=Rex%20%28REX-7%29
}

func TestQuote(t *testing.T) {
	tests := map[string]struct {
		text string
		want string
	}{
		"unreserved characters stay":   {"AZaz09-._~", "AZaz09-._~"},
		"so does the slash":            {"a/b/c", "a/b/c"},
		"what divides a URL":           {":?#[]@", "%3A%3F%23%5B%5D%40"},
		"what divides a query":         {"!$&'()*+,;=", "%21%24%26%27%28%29%2A%2B%2C%3B%3D"},
		"a space is no plus":           {"Rex Senior", "Rex%20Senior"},
		"a plus is none either":        {"a+b", "a%2Bb"},
		"the percent sign and quotes":  {`100% "wool"`, "100%25%20%22wool%22"},
		"other ASCII punctuation":      {"<a|b>\\^`{}", "%3Ca%7Cb%3E%5C%5E%60%7B%7D"},
		"control characters":           {"tab\tnew\nline", "tab%09new%0Aline"},
		"two bytes of UTF-8":           {"Zoë", "Zo%C3%AB"},
		"three":                        {"größe 5€", "gr%C3%B6%C3%9Fe%205%E2%82%AC"},
		"four":                         {"🐾", "%F0%9F%90%BE"},
		"a script without ASCII":       {"猫", "%E7%8C%AB"},
		"nothing":                      {"", ""},
		"what is escaped already":      {"a%20b", "a%2520b"},
		"bytes that are no UTF-8 text": {"\xff\x00", "%FF%00"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := engine.Quote(test.text); got != test.want {
				t.Errorf("Quote(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}

func TestLink(t *testing.T) {
	const (
		toy   = `{"id":"0a0a0a0a","tag":"T-7","title":"Squeaky bone","petId":"0a1b2c3d"}`
		query = `{url: open/toy, query: {pet: "${petId}", title: "${title}"}}`
	)
	tests := map[string]struct {
		// template is the link template of toys, as YAML.
		template    string
		environment string
		item        string
		want        string
	}{
		"a parameter in the path and a query": {
			template: `{url: "https://toys.example.test/${parameter}", parameter: "${shelf}/${photo.path}", query: {title: "${title}", note: "${note}"}}`,
			item:     `{"shelf":"top shelf","title":"Squeaky bone","extra":{"photo":{"path":"toys/bone.png"}}}`,
			want:     "https://toys.example.test/top%20shelf/toys/bone.png?title=Squeaky%20bone",
		},
		"a URL with a host stays as it is": {
			template: `{url: "https://toys.example.test/open", query: {pet: "${petId}"}}`,
			item:     toy,
			want:     "https://toys.example.test/open?pet=0a1b2c3d",
		},
		"a URL without one goes to the link base": {
			template: query,
			item:     toy,
			want:     "https://shop.example.test/open/toy?pet=0a1b2c3d&title=Squeaky%20bone",
		},
		"of the environment": {
			template:    query,
			environment: "staging",
			item:        toy,
			want:        "https://shop.staging.example.test/open/toy?pet=0a1b2c3d&title=Squeaky%20bone",
		},
		"a slash in front changes nothing": {
			template: `{url: /open/toy}`,
			item:     toy,
			want:     "https://shop.example.test/open/toy",
		},
		"what merely starts like a scheme is no host": {
			template: `{url: httpd/status}`,
			item:     toy,
			want:     "https://shop.example.test/httpd/status",
		},
		// The values come from the item, from the objects in it and from
		// what was added to it, and the parameters keep their order.
		"the query in the order of the definition": {
			template: `{url: open/toy, query: {toy: "${id}", shelf: "${shelfId}", owner: "${ownerId}", maker: "${maker.id}", title: "${title} (${tag})"}}`,
			item:     `{"id":"0a0a0a0a","tag":"T-7","title":"Squeaky bone","maker":{"id":"0b0b0b0b","name":"Bones"},"extra":{"ownerId":"0c0c0c0c","shelfId":""}}`,
			want:     "https://shop.example.test/open/toy?toy=0a0a0a0a&owner=0c0c0c0c&maker=0b0b0b0b&title=Squeaky%20bone%20%28T-7%29",
		},
		"zero and false are values, null and nothing are not": {
			template: `{url: open/toy, query: {stock: "${stock}", sold: "${sold}", gone: "${gone}", none: "${none}", blank: "${blank}"}}`,
			item:     `{"stock":0,"sold":false,"gone":null,"blank":""}`,
			want:     "https://shop.example.test/open/toy?stock=0&sold=false",
		},
		"a number as it is spelled": {
			template: `{url: open/toy, query: {price: "${price}"}}`,
			item:     `{"price":2.50}`,
			want:     "https://shop.example.test/open/toy?price=2.50",
		},
		"the whole path language": {
			template: `{url: open/toy, query: {price: "${prices[currency=EUR].amount}"}}`,
			item:     `{"prices":[{"currency":"USD","amount":3},{"currency":"EUR","amount":2}]}`,
			want:     "https://shop.example.test/open/toy?price=2",
		},
		"placeholders without braces and a literal dollar": {
			template: `{url: open/toy, query: {title: "$title", price: "$$${price}"}}`,
			item:     `{"title":"Squeaky bone","price":5}`,
			want:     "https://shop.example.test/open/toy?title=Squeaky%20bone&price=%245",
		},
		// What the item puts into the parameter is escaped. What the
		// template spells out is not, and a query the URL has already is
		// added to.
		"only the values of the parameter are escaped": {
			template: `{url: "https://toys.example.test/${parameter}", parameter: "toys/${shelf}/${title}?x=1", query: {pet: "${petId}"}}`,
			item:     `{"shelf":"a/b","title":"50% off?","petId":"0a1b2c3d"}`,
			want:     "https://toys.example.test/toys/a/b/50%25%20off%3F?x=1&pet=0a1b2c3d",
		},
		"an encoded parameter is rendered as it is and then encoded": {
			template: `{url: "https://toys.example.test/open/${parameter}", parameter: '{"toy":"${id}","title":"${title}"}', encodeParameter: true}`,
			item:     `{"id":"0a0a0a0a","title":"Squeaky bone & ball"}`,
			want:     "https://toys.example.test/open/eyJ0b3kiOiIwYTBhMGEwYSIsInRpdGxlIjoiU3F1ZWFreSBib25lICYgYmFsbCJ9",
		},
		"a URL without the placeholder has no place for the parameter": {
			template: `{url: "https://toys.example.test/open", parameter: "${id}"}`,
			item:     toy,
			want:     "https://toys.example.test/open",
		},
		"a placeholder without a parameter stays": {
			template: `{url: "https://toys.example.test/${parameter}"}`,
			item:     toy,
			want:     "https://toys.example.test/${parameter}",
		},
		"white space around a value is dropped": {
			template: `{url: "https://toys.example.test/${parameter}", parameter: " ${id} ", query: {title: "  ${title}  ", blank: "   "}}`,
			item:     toy,
			want:     "https://toys.example.test/0a0a0a0a?title=Squeaky%20bone",
		},
		"an item with nothing in it has no link": {
			template: query,
			item:     `{}`,
			want:     "",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := projecting(t, "resources:\n  toys:\n    urlTemplate: "+test.template+"\n",
				func(_ *definition.Document, o *engine.Options) { o.Environment = test.environment })
			item, _ := decode(t, test.item).(*apikit.Object)
			if got := shop.Link("toys", item); got != test.want {
				t.Errorf("Link = %q, want %q", got, test.want)
			}
		})
	}

	t.Run("a resource without a template has no link", func(t *testing.T) {
		shop := projecting(t, "resources:\n  toys:\n    urlTemplate: "+query+"\n", nil)
		item, _ := decode(t, toy).(*apikit.Object)
		for _, resource := range []string{"pets", "leashes"} {
			if got := shop.Link(resource, item); got != "" {
				t.Errorf("Link(%s) = %q, want none", resource, got)
			}
		}
		if got := shop.Link("toys", nil); got != "" {
			t.Errorf("Link of no item = %q, want none", got)
		}
	})

	t.Run("without a link base the link has no host", func(t *testing.T) {
		shop := projecting(t, "resources:\n  toys:\n    urlTemplate: "+query+"\n",
			func(doc *definition.Document, _ *engine.Options) { doc.Environments[0].LinkBase = "" })
		item, _ := decode(t, toy).(*apikit.Object)
		if got, want := shop.Link("toys", item), "/open/toy?pet=0a1b2c3d&title=Squeaky%20bone"; got != want {
			t.Errorf("Link = %q, want %q", got, want)
		}
	})
}
