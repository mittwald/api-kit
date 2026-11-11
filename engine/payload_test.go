package engine_test

import (
	"strings"
	"testing"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/engine"
)

// payloadOverlay gives the toys of the pet shop calls whose payloads show
// every rule of a payload template.
const payloadOverlay = `
resources:
  toys:
    setters:
      - name: shape
        method: PUT
        pathTemplate: "toys/${id}"
        payloadTemplate:
          label: "${label}"
          size: "${size}"
          loud: "${loud}"
          tags: "${tags}"
          spaced: "  ${size} "
          text: "size ${size}, loud: ${loud}, for ${tags}"
          price: "$$5 for $label, not $nobody"
          fixed: {kind: toy, since: 2026, empty: {}, none: [], nil: null}
          parts: ["${first}", "${second}"]
          nested: {deep: [{deeper: "${first}"}]}
          "${key}-color": "${color}"
        parameters:
          - {name: label}
          - {name: size, type: int, required: false}
          - {name: loud, type: bool, required: false}
          - {name: tags, type: list, required: false}
          - {name: first, required: false}
          - {name: second, required: false}
          - {name: key, required: false}
          - {name: color, required: false}
        message: "Shaped ${changes}; loud ${loud}, size ${size}, color [${color}], key ${key}, ${nobody}"
      - name: stamp
        method: PUT
        pathTemplate: "toys/${id}/stamp"
        payloadTemplate: |
          {"label": "${label}", "count": ${count}, "ratio": ${ratio}, "loud": ${loud}, "tags": ${tags},
           "note": "say \"${note}\" for $$1", "${key}": [${count}, "${count}", " ${count} "],
           "literal": "$${count} {} []", "braces": {"in": "a } and a { and a \\"}}
        parameters:
          - {name: label}
          - {name: count, type: int}
          - {name: ratio, type: float, required: false}
          - {name: loud, type: bool, required: false}
          - {name: tags, type: list, required: false}
          - {name: note, required: false}
          - {name: key, required: false}
      - name: paint
        pathTemplate: "toys/${id}/paint"
        payloadTemplate: {color: "${color}", gloss: {level: "${gloss}"}}
        parameters:
          - {name: color, required: false}
          - {name: gloss, type: int, required: false}
      - name: tie
        pathTemplate: "toys/${id}/tie"
        payloadTemplate: {knot: "${knot}", color: "${color}"}
        parameters:
          - {name: ribbon, required: false, variables: {knot: bow}}
          - {name: color}
      - name: pile
        pathTemplate: "toys/${id}/pile"
        payloadTemplate: ["${first}", "${second}", 3]
        parameters:
          - {name: first, required: false}
          - {name: second, required: false}
      - name: reset
        pathTemplate: "toys/${id}/reset"
        payloadTemplate: {}
      - name: clear
        pathTemplate: "toys/${id}/clear"
        payloadTemplate: ""
`

// toy is the ID of the toy the tests of payloads change.
const toy = "0f0f0f0f"

func TestPayloadWrittenAsMapping(t *testing.T) {
	const fixed = `"fixed":{"kind":"toy","since":2026,"empty":{},"none":[],"nil":null}`
	tests := map[string]struct {
		params map[string]any
		want   string
	}{
		// Whatever depends on an optional parameter that was not given
		// goes, and with it the list and the objects that hold nothing
		// else. What the template spells out stays, empty or not.
		"an optional parameter that is not given takes its key along": {
			map[string]any{"label": "ball"},
			`{"label":"ball","price":"$5 for ball, not $nobody",` + fixed + `}`,
		},
		// A placeholder that stands alone passes the value on as it is;
		// inside a text every value is shown as text.
		"a placeholder alone keeps the type of its value": {
			map[string]any{"label": "ball", "size": int64(3), "loud": true, "tags": []string{"dogs", "cats"}},
			`{"label":"ball","size":3,"loud":true,"tags":["dogs","cats"],"spaced":3,` +
				`"text":"size 3, loud: true, for [\"dogs\", \"cats\"]","price":"$5 for ball, not $nobody",` + fixed + `}`,
		},
		"a list keeps the items that have a value": {
			map[string]any{"label": "ball", "second": "bell"},
			`{"label":"ball","price":"$5 for ball, not $nobody",` + fixed + `,"parts":["bell"]}`,
		},
		"objects and lists are filled in depth": {
			map[string]any{"label": "ball", "first": "rope", "second": "bell"},
			`{"label":"ball","price":"$5 for ball, not $nobody",` + fixed + `,"parts":["rope","bell"],"nested":{"deep":[{"deeper":"rope"}]}}`,
		},
		"a key is a template": {
			map[string]any{"label": "ball", "key": "fur", "color": "red"},
			`{"label":"ball","price":"$5 for ball, not $nobody",` + fixed + `,"fur-color":"red"}`,
		},
		"a key without its value goes": {
			map[string]any{"label": "ball", "key": "fur"},
			`{"label":"ball","price":"$5 for ball, not $nobody",` + fixed + `}`,
		},
		"a value without its key goes": {
			map[string]any{"label": "ball", "color": "red"},
			`{"label":"ball","price":"$5 for ball, not $nobody",` + fixed + `}`,
		},
		// A value is filled in once and never read as a template or as
		// JSON.
		"a value is text, whatever it holds": {
			map[string]any{"label": "a \"quoted\" ${size} $$\n{\"x\":1}"},
			`{"label":"a \"quoted\" ${size} $$\n{\"x\":1}","price":"$5 for a \"quoted\" ${size} $$\n{\"x\":1}, not $nobody",` + fixed + `}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, payloadOverlay)
			shop.api.Handle("PUT", "/v1/toys/"+toy, noContent)
			if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "shape", Params: test.params}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, "PUT "+mainAPI+"/toys/"+toy+" "+test.want)
		})
	}
}

func TestPayloadWrittenAsJSONText(t *testing.T) {
	const always = `"literal":"${count} {} []","braces":{"in":"a } and a { and a \\"}`
	tests := map[string]struct {
		params map[string]any
		want   string
	}{
		// Between quotes a placeholder is part of a string, outside of
		// them it stands for a value of its own type.
		"values go into the strings and the places for values": {
			map[string]any{"label": "ball", "count": int64(3), "ratio": 0.5, "loud": true, "tags": []string{"dogs"}, "note": "woof", "key": "counts"},
			`{"label":"ball","count":3,"ratio":0.5,"loud":true,"tags":["dogs"],"note":"say \"woof\" for $1","counts":[3,"3"," 3 "],` + always + `}`,
		},
		"an optional parameter that is not given takes its key along": {
			map[string]any{"label": "ball", "count": int64(3)},
			`{"label":"ball","count":3,` + always + `}`,
		},
		// The text is never put together and read again, so a value can
		// neither break the payload nor add a key to it.
		"a quote in a value is a quote": {
			map[string]any{"label": `say "hi"`, "count": int64(1), "note": `a "b" \ c`},
			`{"label":"say \"hi\"","count":1,"note":"say \"a \"b\" \\ c\" for $1",` + always + `}`,
		},
		"a value cannot add a key": {
			map[string]any{"label": `x", "admin": true, "y": "`, "count": int64(1)},
			`{"label":"x\", \"admin\": true, \"y\": \"","count":1,` + always + `}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, payloadOverlay)
			shop.api.Handle("PUT", "/v1/toys/"+toy+"/stamp", noContent)
			if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "stamp", Params: test.params}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, "PUT "+mainAPI+"/toys/"+toy+"/stamp "+test.want)
		})
	}
}

func TestPayloadThatCannotBeRead(t *testing.T) {
	tests := map[string]string{
		"no JSON":                              `'{"a": ${a}'`,
		"a plain name outside a string":        `'{"a": $a}'`,
		"a placeholder without a name":         `'{"a": ${}}'`,
		"a placeholder that is never closed":   `'["${a}", ${a]'`,
		"a string that reads like a stand-in":  `'{"a": ${a}, "b": "\u00000"}'`,
		"a second value":                       `'{"a": ${a}} {}'`,
		"a placeholder where a key belongs to": `'{${a}: 1, "b"}'`,
	}
	for name, template := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, `
resources:
  toys:
    setters:
      - name: stamp
        payloadTemplate: `+template+`
        parameters:
          - {name: a}
`)
			_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "stamp", Params: map[string]any{"a": "1"}})
			// A mistake of the definition, found before anything is sent.
			wantError(t, err, apikit.KindDefinition, "the payload template of setter stamp of toys is no JSON with placeholders: ")
			wantRequests(t, shop.api)
		})
	}

	t.Run("a placeholder may be a key", func(t *testing.T) {
		shop := callShop(t, nil, `
resources:
  toys:
    setters:
      - name: stamp
        payloadTemplate: '{${a}: 1, "\u00001": "${a}"}'
        parameters:
          - {name: a}
`)
		shop.api.Handle("POST", "/v1/toys/"+toy, noContent)
		if _, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "stamp", Params: map[string]any{"a": "x"}}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		wantRequests(t, shop.api, "POST "+mainAPI+"/toys/"+toy+` {"x":1,"\u00001":"x"}`)
	})
}

func TestPayloadNeedsItsValues(t *testing.T) {
	tests := map[string]struct {
		call    string
		params  map[string]any
		message string
		// sent is the request, for a payload that can be sent.
		sent string
	}{
		// Sending {} would change nothing and look like a success.
		"a payload of which nothing is left": {
			call: "paint", message: "paint needs at least one of --color, --gloss",
		},
		"one optional parameter is enough": {
			call: "paint", params: map[string]any{"gloss": int64(2)},
			sent: "POST " + mainAPI + "/toys/" + toy + `/paint {"gloss":{"level":2}}`,
		},
		// The knot is set by --ribbon, which may be left out, but the
		// payload cannot do without it.
		"a value that nothing gave": {
			call: "tie", params: map[string]any{"color": "red"},
			message: "tie cannot send its payload without a value for knot",
		},
		"the same value, given": {
			call: "tie", params: map[string]any{"color": "red", "ribbon": "silk"},
			sent: "POST " + mainAPI + "/toys/" + toy + `/tie {"knot":"bow","color":"red"}`,
		},
		"a list that loses all its items but one": {
			call: "pile",
			sent: "POST " + mainAPI + "/toys/" + toy + `/pile [3]`,
		},
		"a list": {
			call: "pile", params: map[string]any{"second": "b", "first": "a"},
			sent: "POST " + mainAPI + "/toys/" + toy + `/pile ["a","b",3]`,
		},
		// The template says so; nothing was dropped.
		"an empty object that is spelled out is sent": {
			call: "reset",
			sent: "POST " + mainAPI + "/toys/" + toy + `/reset {}`,
		},
		"an empty text is no payload": {
			call: "clear",
			sent: "POST " + mainAPI + "/toys/" + toy + `/clear`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, payloadOverlay)
			if test.sent == "" {
				_, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: test.call, Params: test.params})
				wantError(t, err, apikit.KindUsage, test.message)
				wantRequests(t, shop.api)
				return
			}
			shop.api.Handle("POST", "/v1/toys/"+toy+"/"+test.call, noContent)
			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: test.call, Params: test.params})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantRequests(t, shop.api, test.sent)
			// Without a message of its own a setter names what it sent,
			// unless that is nothing.
			says := "Successfully applied toy " + toy
			if _, payload, _ := strings.Cut(test.sent, test.call+" "); payload != "" && payload != "{}" {
				says = "Successfully applied '" + apikit.Text(decode(t, payload)) + "' to toy " + toy
			}
			wantMessages(t, result, says)
		})
	}
}

func TestMessageSeesThePayload(t *testing.T) {
	tests := map[string]struct {
		params map[string]any
		want   string
	}{
		// ${changes} lists what was sent, each value as text. A flag
		// that the payload asked for shows as text, and what it asked
		// for in vain as nothing. A name it never came to, and one the
		// call does not know, stay as they are written.
		"what was sent": {
			map[string]any{"label": "ball", "size": int64(3), "loud": true, "tags": []string{"dogs"}, "first": "rope"},
			`Shaped label=ball, size=3, loud=true, tags=["dogs"], spaced=3, text=size 3, loud: true, for ["dogs"], ` +
				`price=$5 for ball, not $nobody, fixed={"kind": "toy", "since": 2026, "empty": {}, "none": [], "nil": null}, ` +
				`parts=["rope"], nested={"deep": [{"deeper": "rope"}]}; loud true, size 3, color [], key ${key}, ${nobody}`,
		},
		"what was left out": {
			map[string]any{"label": "ball"},
			`Shaped label=ball, price=$5 for ball, not $nobody, fixed={"kind": "toy", "since": 2026, "empty": {}, "none": [], "nil": null}; ` +
				`loud , size , color [], key ${key}, ${nobody}`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := callShop(t, nil, payloadOverlay)
			shop.api.Handle("PUT", "/v1/toys/"+toy, noContent)
			result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "shape", Params: test.params})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			wantMessages(t, result, test.want)
		})
	}

	// A payload that is a list has no fields to list.
	t.Run("a list has no changes", func(t *testing.T) {
		shop := callShop(t, nil, `
resources:
  toys:
    setters:
      - name: pile
        payloadTemplate: ["${first}"]
        parameters:
          - {name: first}
        message: "Piled ${changes} on ${id}"
`)
		shop.api.Handle("POST", "/v1/toys/"+toy, apitest.Response{Raw: `""`})
		result, err := shop.Apply(t.Context(), engine.CallInput{Resource: "toys", ID: toy, Call: "pile", Params: map[string]any{"first": "a"}})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		// An answer that is an empty text counts as none.
		wantMessages(t, result, "Piled ${changes} on "+toy)
	})
}
