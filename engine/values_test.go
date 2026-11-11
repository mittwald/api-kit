package engine_test

import (
	"testing"

	"github.com/mittwald/api-kit/engine"
)

// valuesOverlay gives the toys a call whose path and message show which
// values the templates of a definition see.
const valuesOverlay = `
resources:
  toys:
    setters:
      - name: tag
        method: PUT
        pathTemplate: "toys/${id}/tags/${tag}/${size}"
        parameters:
          - {name: tag}
          - {name: size, type: int}
          - {name: loud, type: bool, required: false}
          - {name: colors, type: list, required: false}
          - {name: shop-name, required: false}
        message: "Tagged ${id} as $tag (${tag}), size ${size}, loud ${loud}, colors ${colors}, shop ${shop_name}/${shop-name}, for $$5, ${nobody} $nobody, ${resource} ${call} ${command}"
`

func TestTemplatesOfADefinition(t *testing.T) {
	tests := map[string]struct {
		params  map[string]any
		path    string
		message string
	}{
		// A text and a number are filled in. A flag and a list have no
		// spelling of their own in a path or a message, so their
		// placeholders stay, as do those of names the call does not know:
		// nothing but its own values reaches a template. A name is
		// written with dashes or with underscores.
		"texts and numbers": {
			map[string]any{"tag": "soft", "size": int64(3), "loud": true, "colors": []string{"red"}, "shop-name": "north"},
			"/tags/soft/3",
			"Tagged " + toy + " as soft (soft), size 3, loud ${loud}, colors ${colors}, shop north/north, for $5, ${nobody} $nobody, ${resource} ${call} ${command}",
		},
		"a value that was not given": {
			map[string]any{"tag": "soft", "size": int64(3)},
			"/tags/soft/3",
			"Tagged " + toy + " as soft (soft), size 3, loud ${loud}, colors ${colors}, shop ${shop_name}/${shop-name}, for $5, ${nobody} $nobody, ${resource} ${call} ${command}",
		},
		// A value in a path is one segment of it, whatever it holds.
		"a value cannot lead to another path": {
			map[string]any{"tag": "a/b?c=d#e f", "size": int64(3)},
			"/tags/a%2Fb%3Fc=d%23e%20f/3",
			"Tagged " + toy + " as a/b?c=d#e f (a/b?c=d#e f), size 3, loud ${loud}, colors ${colors}, shop ${shop_name}/${shop-name}, for $5, ${nobody} $nobody, ${resource} ${call} ${command}",
		},
		"nor climb out of its own": {
			map[string]any{"tag": "..", "size": int64(3)},
			"/tags/%2E%2E/3",
			"Tagged " + toy + " as .. (..), size 3, loud ${loud}, colors ${colors}, shop ${shop_name}/${shop-name}, for $5, ${nobody} $nobody, ${resource} ${call} ${command}",
		},
		"a value that looks like a placeholder is a value": {
			map[string]any{"tag": "${size}", "size": int64(3)},
			"/tags/$%7Bsize%7D/3",
			"Tagged " + toy + " as ${size} (${size}), size 3, loud ${loud}, colors ${colors}, shop ${shop_name}/${shop-name}, for $5, ${nobody} $nobody, ${resource} ${call} ${command}",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, valuesOverlay)
			shop.api.Handle("PUT", "/v1/toys/"+toy+test.path, noContent)

			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "tag", Params: test.params})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, "PUT "+mainAPI+"/toys/"+toy+test.path)
			wantMessages(t, result, test.message)
		})
	}
}
