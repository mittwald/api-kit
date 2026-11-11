package engine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// kitchen adds to the pet shop what the tests of joins need besides the
// resource of the items they project, the feeding plans: recipes belong
// to foods, and a kennel keeps its ID under a name of its own.
const kitchen = `
resources:
  recipes: {parent: foods}
  kennels: {idField: code}
`

// Related items of the pet shop, and the paths they are fetched at.
const (
	bobID   = "0e0e0e0e"
	kittyID = "0d0d0d0d"

	adaPath     = "/v1/owners/" + owner
	bobPath     = "/v1/owners/" + bobID
	kibblePath  = "/v1/foods/f-1"
	recipesPath = "/v1/foods/f-1/recipes"
	foxPath     = "/v1/vets/d-1"

	adaJSON     = `{"id":"` + owner + `","name":"Ada","city":"Bern","vetId":"d-1","pets":0,"address":{"street":"Main St"}}`
	bobJSON     = `{"id":"` + bobID + `","name":"Bob","city":"Basel"}`
	kibbleJSON  = `{"id":"f-1","name":"Kibble","tags":["dry"],"maker":"Bones"}`
	recipesJSON = `[{"id":"r-1","title":"Classic","vegan":false,"until":null},{"id":"r-2","title":"Green","vegan":true,"until":"2030-06-30"}]`
	foxJSON     = `{"id":"d-1","name":"Dr. Fox"}`
	ballJSON    = `{"id":"t-1","name":"Ball","color":"red"}`
	boneJSON    = `{"id":"t-2","name":"Bone","color":"white"}`

	// byOwner joins the name of the owner of a plan.
	byOwner = `{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, join: name}]}`
	// byToys joins the names of the toys of a plan.
	byToys = `{columns: [toys], joins: [{name: toys, column: toyIds, resource: toys, join: name}]}`
	// byRelation joins the buyer, the subject and the carer that the
	// relation entries of an order name.
	byRelation = `{columns: [buyer, subject, carer], joins: [` +
		`{name: buyer, column: [parties, about, handledBy], conditions: {kind: owner, role: buyer}, resource: owners, join: name}, ` +
		`{name: subject, column: [parties, about, handledBy], conditions: {kind: pet, role: subject}, resource: pets, join: name}, ` +
		`{name: carer, column: [parties, about, handledBy], conditions: {kind: vet, role: carer}, resource: vets, join: name}]}`
)

// everyJoin asks for all joins.
var everyJoin = engine.ProjectOptions{Joins: true, JoinMany: true}

// joining returns an engine for the pet shop in which plans are what the
// given YAML says, and scripts the answers to the GET requests for the
// given paths. Joins run one at a time, so that requests are sent in the
// order of the rows and columns.
func joining(t *testing.T, plans string, routes map[string]apitest.Response) *shop {
	t.Helper()
	shop := projecting(t, kitchen+"  plans: "+plans+"\n", func(doc *definition.Document, _ *engine.Options) {
		doc.HTTP.JoinMaxWorkers, doc.HTTP.Retries = 1, 0
	})
	for path, response := range routes {
		shop.api.Handle("GET", path, response)
	}
	return shop
}

// jsonAnswers scripts JSON answers by path: each path is followed by the
// JSON that answers it.
func jsonAnswers(pairs ...string) map[string]apitest.Response {
	routes := map[string]apitest.Response{}
	for i := 1; i < len(pairs); i += 2 {
		routes[pairs[i-1]] = apitest.Response{Raw: pairs[i]}
	}
	return routes
}

func TestJoin(t *testing.T) {
	const (
		visitsOfRex   = "/v1/pets/" + rex + "/visits"
		visitsOfKitty = "/v1/pets/" + kittyID + "/visits"
		twoVisits     = `[{"id":"d-1","reason":"checkup","date":"2026-01-05","vetId":"d-1"},{"id":"d-2","reason":"shot"}]`
		byVisits      = `{columns: [visits], joins: [{name: visits, column: petIds, resource: visits, join: reason}]}`
		// byFood joins the food of a plan with one of its recipes.
		byFood = `{columns: [food], joins: [{name: food, column: foodId, resource: foods, join: [name, tags], ` +
			`nested: {name: recipe, column: recipe.now, resource: recipes, join: [id, title, vegan]}}]}`
		// threeRecipes joins the food of a plan with three of its recipes.
		threeRecipes = `joins: [{name: food, column: foodId, resource: foods, join: [name, tags], nested: [` +
			`{name: recipe, column: recipe.now, resource: recipes, join: [id, title]}, ` +
			`{name: nextRecipe, column: recipe.next, resource: recipes, join: [id, title]}, ` +
			`{name: lastRecipe, column: recipe.last, resource: recipes, join: [id, title]}]}]`
		withVet = `{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, join: [name, vetId], ` +
			`nested: {name: vet, column: vetId, resource: vets, join: name}}]}`
	)
	tests := map[string]struct {
		// above is YAML that comes before the resources of the kitchen.
		above string
		// plans describes the resource of the items, as YAML.
		plans   string
		items   string
		routes  map[string]apitest.Response
		options *engine.ProjectOptions
		// rows are the values of the table, and sent the requests in
		// their order, without the "GET".
		rows string
		sent []string
	}{
		// One related item.
		"one field of the related item": {
			plans: byOwner, items: `[{"id":"p-1","ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"several fields make an object, the ID first": {
			plans:  `{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, join: [name, city, phone]}]}`,
			items:  `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[[{"id":"` + owner + `","name":"Ada","city":"Bern","phone":null}]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"a field may be a path": {
			plans: `{columns: [street, owner], joins: [{name: street, column: ownerId, resource: owners, join: address.street}, ` +
				`{name: owner, column: ownerId, resource: owners, join: [name, address.street]}]}`,
			items:  `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Main St",{"id":"` + owner + `","name":"Ada","address.street":"Main St"}]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"the ID is found where the related resource keeps it": {
			plans: `{columns: [kennel, vet], joins: [{name: kennel, column: kennelId, resource: kennels, join: [name, size]}, ` +
				`{name: vet, column: vetId, resource: vets, join: [name, phone]}]}`,
			items:  `[{"kennelId":"k-1","vetId":"d-1"}]`,
			routes: jsonAnswers("/v1/kennels/k-1", `{"code":"k-1","name":"North","size":3}`, foxPath, `{"vetId":"d-1","name":"Dr. Fox"}`),
			rows:   `[[{"id":"k-1","name":"North","size":3},{"id":"d-1","name":"Dr. Fox","phone":null}]]`,
			sent:   []string{mainAPI + "/kennels/k-1", mainAPI + "/vets/d-1"},
		},
		"a path below the join leads into the related item": {
			plans: `{columns: [owner.city, owner.phone, owner.pets, owner.address.street, owner.address], ` +
				`joins: [{name: owner, column: ownerId, resource: owners, join: name}]}`,
			items:  `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Bern","",0,"Main St",{"street":"Main St"}]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"a related item without the field shows nothing": {
			plans:  `{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, join: phone}]}`,
			items:  `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[[""]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"a join without fields shows only what a path asks for": {
			plans:  `{columns: [owner, owner.name], joins: [{name: owner, column: ownerId, resource: owners}]}`,
			items:  `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["","Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"a related item that is no object shows as text": {
			plans: `{columns: [state, both], joins: [{name: state, column: ownerId, resource: owners, join: name}, ` +
				`{name: both, column: ownerId, resource: owners, join: [name, city]}]}`,
			items:  `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, `"moved away"`),
			rows:   `[["moved away",""]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"an answer that holds nothing is no related item": {
			plans: byOwner, items: `[{"ownerId":"` + owner + `"},{"ownerId":"` + bobID + `"}]`,
			routes: map[string]apitest.Response{adaPath: {Raw: `{}`}, bobPath: {Status: 204}},
			rows:   `[[""],[""]]`, sent: []string{mainAPI + "/owners/" + owner, mainAPI + "/owners/" + bobID},
		},

		// The IDs.
		"a list of IDs makes a list": {
			plans: byToys, items: `[{"toyIds":["t-1","t-2"]}]`,
			routes: jsonAnswers("/v1/toys/t-1", ballJSON, "/v1/toys/t-2", boneJSON),
			rows:   `[[["Ball","Bone"]]]`, sent: []string{mainAPI + "/toys/t-1", mainAPI + "/toys/t-2"},
		},
		"one ID in a list is one value": {
			plans: byToys, items: `[{"toyIds":["t-1"]}]`,
			routes: jsonAnswers("/v1/toys/t-1", ballJSON),
			rows:   `[["Ball"]]`, sent: []string{mainAPI + "/toys/t-1"},
		},
		"an ID that is there twice is joined twice": {
			plans: byToys, items: `[{"toyIds":["t-1","t-1"]}]`,
			routes: jsonAnswers("/v1/toys/t-1", ballJSON),
			rows:   `[[["Ball","Ball"]]]`, sent: []string{mainAPI + "/toys/t-1"},
		},
		"what is no ID is passed over": {
			plans: byToys, items: `[{"toyIds":["",null,{"id":"t-9"},["t-8"],true,"t-2"]}]`,
			routes: jsonAnswers("/v1/toys/t-2", boneJSON),
			rows:   `[["Bone"]]`, sent: []string{mainAPI + "/toys/t-2"},
		},
		"a number is an ID, zero is none": {
			plans: byToys, items: `[{"toyIds":[7,0]},{"toyIds":8}]`,
			routes: jsonAnswers("/v1/toys/7", `{"id":7,"name":"Seven"}`, "/v1/toys/8", `{"id":8,"name":"Eight"}`),
			rows:   `[["Seven"],["Eight"]]`, sent: []string{mainAPI + "/toys/7", mainAPI + "/toys/8"},
		},
		// An empty ID would name the whole listing of the resource.
		"an item without an ID asks for nothing": {
			plans: byToys, items: `[{"toyIds":""},{"toyIds":null},{},{"toyIds":{"id":"t-1"}},{"toyIds":[]}]`,
			rows: `[[""],[""],[""],[""],[""]]`,
		},
		"IDs along a path through a list": {
			plans:  `{columns: [toys], joins: [{name: toys, column: parts.toyId, resource: toys, join: name}]}`,
			items:  `[{"parts":[{"toyId":"t-1"},{"quantity":2},{"toyId":"t-2"}]}]`,
			routes: jsonAnswers("/v1/toys/t-1", ballJSON, "/v1/toys/t-2", boneJSON),
			rows:   `[[["Ball","Bone"]]]`, sent: []string{mainAPI + "/toys/t-1", mainAPI + "/toys/t-2"},
		},
		"an ID that was added to the item": {
			plans: byOwner, items: `[{"extra":{"ownerId":"` + owner + `"}}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"of several related items those that show something": {
			plans: byToys, items: `[{"toyIds":["t-1","t-3","t-4"]}]`,
			routes: jsonAnswers("/v1/toys/t-1", ballJSON, "/v1/toys/t-3", `{"id":"t-3"}`, "/v1/toys/t-4", `{"id":"t-4","name":""}`),
			rows:   `[[["Ball",""]]]`, sent: []string{mainAPI + "/toys/t-1", mainAPI + "/toys/t-3", mainAPI + "/toys/t-4"},
		},

		// Joins to many.
		"a join to many is a list even of one": {
			plans:  `{columns: [toys], joins: [{name: toys, column: toyIds, resource: toys, join: [name, color], many: true}]}`,
			items:  `[{"toyIds":["t-1"]}]`,
			routes: jsonAnswers("/v1/toys/t-1", ballJSON),
			rows:   `[[[` + ballJSON + `]]]`, sent: []string{mainAPI + "/toys/t-1"},
		},
		"and is left alone unless it is asked for": {
			plans:   `{columns: [toys, toys.color], joins: [{name: toys, column: toyIds, resource: toys, join: [name, color], many: true}]}`,
			items:   `[{"toyIds":["t-1"],"toys":"none"}]`,
			options: &engine.ProjectOptions{Joins: true},
			rows:    `[["",""]]`,
		},

		// A related resource that is a sub-resource: the item holds the
		// IDs of the parents.
		"the items of a sub-resource are listed for each parent": {
			plans: byVisits, items: `[{"petIds":["` + rex + `","` + kittyID + `"]}]`,
			routes: jsonAnswers(visitsOfRex, twoVisits, visitsOfKitty, `{"id":"d-3","reason":"surgery"}`),
			rows:   `[[["checkup","shot","surgery"]]]`,
			sent:   []string{adminAPI + "/pets/" + rex + "/visits", adminAPI + "/pets/" + kittyID + "/visits"},
		},
		"they are a list however few there are": {
			plans: byVisits, items: `[{"petIds":"` + kittyID + `"},{"petIds":"` + rex + `"}]`,
			routes: jsonAnswers(visitsOfKitty, `[{"id":"d-3","reason":"surgery"}]`, visitsOfRex, `[]`),
			rows:   `[[["surgery"]],[[]]]`,
			sent:   []string{adminAPI + "/pets/" + kittyID + "/visits", adminAPI + "/pets/" + rex + "/visits"},
		},
		"with several fields each is an object with its own ID": {
			plans:  `{columns: [visits], joins: [{name: visits, column: petIds, resource: visits, join: [reason, date]}]}`,
			items:  `[{"petIds":"` + rex + `"}]`,
			routes: jsonAnswers(visitsOfRex, twoVisits),
			rows:   `[[[{"id":"d-1","reason":"checkup","date":"2026-01-05"},{"id":"d-2","reason":"shot","date":null}]]]`,
			sent:   []string{adminAPI + "/pets/" + rex + "/visits"},
		},
		"a path below the join is taken from each": {
			plans:  `{columns: [visits.date], joins: [{name: visits, column: petIds, resource: visits, join: reason}]}`,
			items:  `[{"petIds":"` + rex + `"}]`,
			routes: jsonAnswers(visitsOfRex, twoVisits),
			rows:   `[[["2026-01-05",""]]]`, sent: []string{adminAPI + "/pets/" + rex + "/visits"},
		},
		"what is listed need not be objects": {
			plans: byVisits, items: `[{"petIds":"` + rex + `"}]`,
			routes: jsonAnswers(visitsOfRex, `["checkup",7,null]`),
			rows:   `[[["checkup","7",""]]]`, sent: []string{adminAPI + "/pets/" + rex + "/visits"},
		},

		// The order of several related items.
		"sorted by several fields, each in its direction": {
			plans: `{columns: [foods], joins: [{name: foods, column: foodIds, resource: foods, join: [name, organic, version], ` +
				`sort: [{column: organic, direction: desc}, {column: version}]}]}`,
			items: `[{"foodIds":["f-1","f-2","f-3","f-4"]}]`,
			routes: jsonAnswers(
				"/v1/foods/f-1", `{"id":"f-1","name":"Kibble","organic":false,"version":"1.10"}`,
				"/v1/foods/f-2", `{"id":"f-2","name":"Kibble","organic":true,"version":"1.9"}`,
				"/v1/foods/f-3", `{"id":"f-3","name":"Kibble","organic":false,"version":"1.9"}`,
				"/v1/foods/f-4", `{"id":"f-4","name":"Kibble","organic":true,"version":"1.10"}`),
			rows: `[[[` +
				`{"id":"f-2","name":"Kibble","organic":true,"version":"1.9"},` +
				`{"id":"f-4","name":"Kibble","organic":true,"version":"1.10"},` +
				`{"id":"f-3","name":"Kibble","organic":false,"version":"1.9"},` +
				`{"id":"f-1","name":"Kibble","organic":false,"version":"1.10"}]]]`,
			sent: []string{mainAPI + "/foods/f-1", mainAPI + "/foods/f-2", mainAPI + "/foods/f-3", mainAPI + "/foods/f-4"},
		},
		"the values of one field are sorted by themselves, as a person reads them": {
			plans:  `{columns: [editions], joins: [{name: editions, column: foodId, resource: recipes, join: [edition], sort: [{column: edition}]}]}`,
			items:  `[{"foodId":"f-1"}]`,
			routes: jsonAnswers(recipesPath, `[{"id":"r-1","edition":"4.0.10"},{"id":"r-2","edition":"4.0.9"},{"id":"r-3","edition":"4.0.11"}]`),
			rows:   `[[["4.0.9","4.0.10","4.0.11"]]]`, sent: []string{mainAPI + "/foods/f-1/recipes"},
		},
		"an order by another field leaves them as they came": {
			plans:  `{columns: [editions], joins: [{name: editions, column: foodId, resource: recipes, join: edition, sort: [{column: title}]}]}`,
			items:  `[{"foodId":"f-1"}]`,
			routes: jsonAnswers(recipesPath, `[{"id":"r-1","edition":"4.0.10","title":"b"},{"id":"r-2","edition":"4.0.9","title":"a"}]`),
			rows:   `[[["4.0.10","4.0.9"]]]`, sent: []string{mainAPI + "/foods/f-1/recipes"},
		},

		// Nested joins go on from the related item. Where the nested
		// resource belongs to the related one, it is among the items
		// listed for the related item.
		"a nested join from an ID that the item holds": {
			plans: byFood, items: `[{"foodId":"f-1","recipe":{"now":"r-2"}}]`,
			routes: jsonAnswers(kibblePath, kibbleJSON, recipesPath, recipesJSON),
			rows:   `[[{"id":"f-1","name":"Kibble","tags":["dry"],"recipe":{"id":"r-2","title":"Green","vegan":true}}]]`,
			sent:   []string{mainAPI + "/foods/f-1", mainAPI + "/foods/f-1/recipes"},
		},
		"nested joins from the IDs that the entries of a list pair": {
			plans: `{columns: [food], joins: [{name: food, column: meals.foodId, resource: foods, join: [name, tags], ` +
				`nested: {name: recipe, column: meals.recipe.chosen, resource: recipes, join: [title, vegan, until]}}]}`,
			items: `[{"meals":[{"foodId":"f-1","recipe":{"chosen":"r-1"},"portion":"small"},{"foodId":"f-2","recipe":{"chosen":"r-9"}}]}]`,
			routes: jsonAnswers(kibblePath, kibbleJSON, recipesPath, recipesJSON,
				"/v1/foods/f-2", `{"id":"f-2","name":"Greens","tags":["fresh"]}`,
				"/v1/foods/f-2/recipes", `[{"id":"r-9","title":"Garden","vegan":true,"until":"2030-06-30"}]`),
			rows: `[[[` +
				`{"id":"f-1","name":"Kibble","tags":["dry"],"recipe":{"id":"r-1","title":"Classic","vegan":false,"until":null}},` +
				`{"id":"f-2","name":"Greens","tags":["fresh"],"recipe":{"id":"r-9","title":"Garden","vegan":true,"until":"2030-06-30"}}]]]`,
			sent: []string{mainAPI + "/foods/f-1", mainAPI + "/foods/f-1/recipes", mainAPI + "/foods/f-2", mainAPI + "/foods/f-2/recipes"},
		},
		"several nested joins": {
			plans:  `{columns: [food], ` + threeRecipes + `}`,
			items:  `[{"foodId":"f-1","recipe":{"next":"r-2","now":"r-2","last":"r-1"}}]`,
			routes: jsonAnswers(kibblePath, kibbleJSON, recipesPath, recipesJSON),
			rows: `[[{"id":"f-1","name":"Kibble","tags":["dry"],"recipe":{"id":"r-2","title":"Green"},` +
				`"nextRecipe":{"id":"r-2","title":"Green"},"lastRecipe":{"id":"r-1","title":"Classic"}}]]`,
			sent: []string{mainAPI + "/foods/f-1", mainAPI + "/foods/f-1/recipes"},
		},
		// The value of a nested join is not given its own nested joins
		// once more.
		"a path into a nested join": {
			plans:  `{columns: [food.nextRecipe.title, food.recipe, food.recipe.missing, food.maker, food.lastRecipe.title], ` + threeRecipes + `}`,
			items:  `[{"foodId":"f-1","recipe":{"next":"r-2","now":"r-1"}}]`,
			routes: jsonAnswers(kibblePath, kibbleJSON, recipesPath, recipesJSON),
			rows:   `[["Green",{"id":"r-1","title":"Classic"},"","Bones",""]]`,
			sent:   []string{mainAPI + "/foods/f-1", mainAPI + "/foods/f-1/recipes"},
		},
		"a nested join from an ID of the related item, fetched on its own": {
			plans: withVet, items: `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON, foxPath, foxJSON),
			rows:   `[[{"id":"` + owner + `","name":"Ada","vetId":"d-1","vet":"Dr. Fox"}]]`,
			sent:   []string{mainAPI + "/owners/" + owner, mainAPI + "/vets/d-1"},
		},
		// An empty ID would name the whole listing of the nested resource.
		"without an ID there is no nested join": {
			plans: withVet, items: `[{"ownerId":"` + bobID + `"}]`,
			routes: jsonAnswers(bobPath, bobJSON),
			rows:   `[[{"id":"` + bobID + `","name":"Bob","vetId":null}]]`, sent: []string{mainAPI + "/owners/" + bobID},
		},
		"a nested item that is not among those listed": {
			plans: byFood, items: `[{"foodId":"f-1","recipe":{"now":"r-7"}}]`,
			routes: jsonAnswers(kibblePath, kibbleJSON, recipesPath, recipesJSON),
			rows:   `[[{"id":"f-1","name":"Kibble","tags":["dry"]}]]`,
			sent:   []string{mainAPI + "/foods/f-1", mainAPI + "/foods/f-1/recipes"},
		},
		"the nested item need not be in a list": {
			plans: byFood, items: `[{"foodId":"f-1","recipe":{"now":"r-2"}},{"foodId":"f-2","recipe":{"now":"r-2"}}]`,
			routes: jsonAnswers(kibblePath, kibbleJSON, recipesPath, `{"id":"r-2","title":"Green","vegan":true}`,
				"/v1/foods/f-2", `{"id":"f-2","name":"Greens"}`, "/v1/foods/f-2/recipes", `["r-2",null,{"id":"r-2","title":"Garden"}]`),
			rows: `[[{"id":"f-1","name":"Kibble","tags":["dry"],"recipe":{"id":"r-2","title":"Green","vegan":true}}],` +
				`[{"id":"f-2","name":"Greens","tags":null,"recipe":{"id":"r-2","title":"Garden","vegan":null}}]]`,
			sent: []string{mainAPI + "/foods/f-1", mainAPI + "/foods/f-1/recipes", mainAPI + "/foods/f-2", mainAPI + "/foods/f-2/recipes"},
		},
		"a join of one field has no place for nested joins": {
			plans: `{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, join: name, ` +
				`nested: {name: vet, column: vetId, resource: vets, join: name}}]}`,
			items:  `[{"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		// The vet of a visit is the visit's, not the one of the item.
		"nested joins of the items of a sub-resource": {
			plans: `{columns: [visits], joins: [{name: visits, column: petId, resource: visits, join: [reason, vetId], ` +
				`nested: {name: vet, column: vetId, resource: vets, join: name}}]}`,
			items:  `[{"petId":"` + rex + `","vetId":"d-0"}]`,
			routes: jsonAnswers(visitsOfRex, twoVisits, foxPath, foxJSON),
			rows: `[[[{"id":"d-1","reason":"checkup","vetId":"d-1","vet":"Dr. Fox"},` +
				`{"id":"d-2","reason":"shot","vetId":null}]]]`,
			sent: []string{adminAPI + "/pets/" + rex + "/visits", mainAPI + "/vets/d-1"},
		},

		// Relations: the item names what it is related to in entries, and
		// the conditions of a join pick the entry.
		"the entry that meets the conditions, in a list or alone": {
			plans: byRelation,
			items: `[{"parties":[{"id":"` + kittyID + `","kind":"pet","role":"gift"},{"id":"` + owner + `","kind":"owner","role":"buyer"}]},` +
				`{"about":{"id":"` + rex + `","kind":"pet","role":"subject"}},` +
				`{"handledBy":{"id":"d-1","kind":"vet","role":"carer"}}]`,
			routes: jsonAnswers(adaPath, adaJSON, "/v1/pets/"+rex, `{"id":"`+rex+`","name":"Rex"}`, foxPath, foxJSON),
			rows:   `[["Ada","",""],["","Rex",""],["","","Dr. Fox"]]`,
			sent:   []string{mainAPI + "/owners/" + owner, mainAPI + "/pets/" + rex, mainAPI + "/vets/d-1"},
		},
		"with several fields and with a path below the join": {
			plans: `{columns: [buyer, buyer.city], joins: [{name: buyer, column: [parties, about], ` +
				`conditions: {kind: owner, role: buyer}, resource: owners, join: [name, city]}]}`,
			items:  `[{"parties":[{"id":"` + owner + `","kind":"owner","role":"buyer"}]}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[[{"id":"` + owner + `","name":"Ada","city":"Bern"},"Bern"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"the first entry that meets the conditions decides": {
			plans: `{columns: [buyer], joins: [{name: buyer, column: [parties, about], ` +
				`conditions: {kind: owner, role: buyer}, resource: owners, join: name}]}`,
			items: `[{"about":{"id":"` + bobID + `","kind":"owner","role":"buyer"},` +
				`"parties":["` + bobID + `",{"id":"` + bobID + `","kind":"owner","role":"seller"},{"kind":"owner","role":"buyer"},{"id":"` + bobID + `","kind":"owner","role":"buyer"}]}]`,
			rows: `[[""]]`,
		},
		"values that are no text are compared as text": {
			plans: `{columns: [buyer], joins: [{name: buyer, column: [parties], ` +
				`conditions: {primary: "true", rank: "1"}, resource: owners, join: name}]}`,
			items:  `[{"parties":[{"id":"` + bobID + `","primary":false,"rank":1},{"id":"` + owner + `","primary":true,"rank":1}]}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"the ID of an entry is where the definition says": {
			above: "columns:\n  id: ref\n",
			plans: `{columns: [buyer], joins: [{name: buyer, column: [parties], ` +
				`conditions: {kind: owner}, resource: owners, join: name}]}`,
			items:  `[{"parties":[{"id":"` + bobID + `","ref":"` + owner + `","kind":"owner"}]}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"one field of entries is named like a path": {
			plans:  `{columns: [buyer], joins: [{name: buyer, column: parties, conditions: {kind: owner}, resource: owners, join: name}]}`,
			items:  `[{"parties":[{"id":"` + owner + `","kind":"owner"}]}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		// No path leads to the ID of a relation, so the nested join
		// starts from the related item.
		"a relation with a nested join": {
			plans: `{columns: [buyer], joins: [{name: buyer, column: [parties], conditions: {kind: owner}, resource: owners, ` +
				`join: [name, vetId], nested: {name: vet, column: vetId, resource: vets, join: name}}]}`,
			items:  `[{"parties":[{"id":"` + owner + `","kind":"owner"}],"vetId":"d-0"}]`,
			routes: jsonAnswers(adaPath, adaJSON, foxPath, foxJSON),
			rows:   `[[{"id":"` + owner + `","name":"Ada","vetId":"d-1","vet":"Dr. Fox"}]]`,
			sent:   []string{mainAPI + "/owners/" + owner, mainAPI + "/vets/d-1"},
		},

		// A column with several paths.
		"a join and a field of the item in one column": {
			plans:  `{columns: [{label: who, paths: [owner, suffix]}], joins: [{name: owner, column: ownerId, resource: owners, join: name}]}`,
			items:  `[{"ownerId":"` + owner + `","suffix":"tail"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada, tail"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"what two joins both find shows once": {
			plans: `{columns: [{label: owner, paths: [pet.ownerId, owner.id]}], joins: [` +
				`{name: pet, column: petId, resource: pets, join: name}, {name: owner, column: ownerId, resource: owners, join: name}]}`,
			items:  `[{"petId":"` + rex + `","ownerId":"` + owner + `"}]`,
			routes: jsonAnswers("/v1/pets/"+rex, `{"id":"`+rex+`","name":"Rex","ownerId":"`+owner+`"}`, adaPath, adaJSON),
			rows:   `[["` + owner + `"]]`, sent: []string{mainAPI + "/pets/" + rex, mainAPI + "/owners/" + owner},
		},
		"a join that finds nothing adds nothing": {
			plans: `{columns: [{label: owner, paths: [pet.ownerId, owner.missing, vet]}], joins: [` +
				`{name: pet, column: petId, resource: pets, join: name}, {name: owner, column: ownerId, resource: owners, join: name}, ` +
				`{name: vet, column: vetId, resource: vets, join: name}]}`,
			items:  `[{"petId":"` + rex + `","ownerId":"` + owner + `"}]`,
			routes: jsonAnswers("/v1/pets/"+rex, `{"id":"`+rex+`","name":"Rex","ownerId":"`+owner+`"}`, adaPath, adaJSON),
			rows:   `[["` + owner + `"]]`, sent: []string{mainAPI + "/pets/" + rex, mainAPI + "/owners/" + owner},
		},

		// A path that names a join is the join's, whatever the item
		// holds under that name.
		"a join comes before a field of the item": {
			plans: byOwner, items: `[{"owner":"old","ownerId":"` + owner + `"},{"owner":{"name":"Old"},"ownerId":"` + owner + `"}]`,
			routes: jsonAnswers(adaPath, adaJSON),
			rows:   `[["Ada"],["Ada"]]`, sent: []string{mainAPI + "/owners/" + owner},
		},
		"where it finds nothing an object of the item shows, a text does not": {
			plans: byOwner, items: `[{"owner":"old"},{"owner":{"name":"Old"}},{"owner":[{"name":"Old"},{"name":"Older"}]}]`,
			rows: `[[""],["Old"],["Old, Older"]]`,
		},
		"without joins the path is a field like any other": {
			plans: byOwner, items: `[{"owner":"old","ownerId":"` + owner + `"}]`,
			options: &engine.ProjectOptions{JoinMany: true},
			rows:    `[["old"]]`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			shop := projecting(t, test.above+kitchen+"  plans: "+test.plans+"\n", func(doc *definition.Document, _ *engine.Options) {
				doc.HTTP.JoinMaxWorkers, doc.HTTP.Retries = 1, 0
			})
			for path, response := range test.routes {
				shop.api.Handle("GET", path, response)
			}
			options := everyJoin
			if test.options != nil {
				options = *test.options
			}
			table, err := shop.Project(t.Context(), "plans", itemsOf(t, test.items), options)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			wantJSON(t, table.Rows, test.rows)
			want := make([]string, len(test.sent))
			for i, url := range test.sent {
				want[i] = "GET " + url
			}
			wantSent(t, shop.api, want...)
		})
	}
}

func TestJoinCompletesItems(t *testing.T) {
	const plans = `{joins: [{name: owner, column: ownerId, resource: owners, join: name}, ` +
		`{name: toys, column: toyIds, resource: toys, join: [name, color], many: true}], ` +
		`urlTemplate: {url: plans, query: {owner: "${owner}", first: "${toys.name}"}}}`
	routes := jsonAnswers(adaPath, adaJSON, "/v1/toys/t-1", ballJSON)

	t.Run("with the values of all joins", func(t *testing.T) {
		shop := joining(t, plans, routes)
		list := itemsOf(t, `[{"id":"p-1","ownerId":"`+owner+`","toyIds":["t-1"]},{"id":"p-2","owner":"old"}]`)
		table, err := shop.Project(t.Context(), "plans", list, engine.ProjectOptions{AllColumns: true, Joins: true, JoinMany: true, Links: true})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if got, want := columnsOf(table), []string{"id", "ownerId", "owner", "toys"}; !slices.Equal(got, want) {
			t.Errorf("columns: %q, want %q", got, want)
		}
		wantJSON(t, table.Rows, `[["p-1","`+owner+`","Ada",[`+ballJSON+`]],["p-2","","",""]]`)
		// The link is made of what the joins found. The second plan holds
		// an owner of its own, which stays.
		wantJSON(t, list, `[`+
			`{"id":"p-1","ownerId":"`+owner+`","toyIds":["t-1"],"extra":{"owner":"Ada","toys":[`+ballJSON+`],`+
			`"link":"https://shop.example.test/plans?owner=Ada&first=%5B%22Ball%22%5D"}},`+
			`{"id":"p-2","owner":"old","extra":{"ownerId":"","toys":"","link":"https://shop.example.test/plans?owner=old"}}]`)
	})

	t.Run("without those to many in a table", func(t *testing.T) {
		shop := joining(t, plans, routes)
		list := itemsOf(t, `[{"id":"p-1","ownerId":"`+owner+`","toyIds":["t-1"]}]`)
		table, err := shop.Project(t.Context(), "plans", list, engine.ProjectOptions{AllColumns: true, Joins: true})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		if got, want := columnsOf(table), []string{"id", "ownerId", "owner"}; !slices.Equal(got, want) {
			t.Errorf("columns: %q, want %q", got, want)
		}
		wantJSON(t, list, `[{"id":"p-1","ownerId":"`+owner+`","toyIds":["t-1"],"extra":{"owner":"Ada"}}]`)
		wantSent(t, shop.api, "GET "+mainAPI+"/owners/"+owner)
	})
}

// Two items that are joined to the same related item share one request,
// and nothing else: what one of them is given can be changed without the
// other, or a later request, seeing it.
func TestJoinKeepsItemsApart(t *testing.T) {
	shop := joining(t, `{columns: [owner, owner.address, toys], joins: [`+
		`{name: owner, column: ownerId, resource: owners, join: [name, address]}, `+
		`{name: toys, column: toyIds, resource: toys, join: tags}]}`,
		jsonAnswers(adaPath, adaJSON, "/v1/toys/t-1", `{"id":"t-1","tags":["chew"]}`))
	list := itemsOf(t, `[{"ownerId":"`+owner+`","toyIds":"t-1"},{"ownerId":"`+owner+`","toyIds":"t-1"}]`)
	table, err := shop.Project(t.Context(), "plans", list, everyJoin)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	const row = `[{"id":"` + owner + `","name":"Ada","address":{"street":"Main St"}},{"street":"Main St"},["chew"]]`
	wantJSON(t, table.Rows, `[`+row+`,`+row+`]`)
	wantSent(t, shop.api, "GET "+mainAPI+"/owners/"+owner, "GET "+mainAPI+"/toys/t-1")

	joined := table.Rows[0][0].(*apikit.Object)
	joined.Set("name", "changed")
	address, _ := joined.Get("address")
	address.(*apikit.Object).Set("street", "changed")
	table.Rows[0][1].(*apikit.Object).Set("street", "changed")
	table.Rows[0][2].([]any)[0] = "changed"

	wantJSON(t, table.Rows[1], row)
	wantJSON(t, list[1], `{"ownerId":"`+owner+`","toyIds":"t-1","extra":{"owner":{"id":"`+owner+`","name":"Ada","address":{"street":"Main St"}},`+
		`"owner.address":{"street":"Main St"},"toys":["chew"]}}`)
	again, err := shop.Fetch(t.Context(), engine.Ref{Resource: "owners", ID: owner}, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	wantJSON(t, again, adaJSON)
}

func TestJoinFailures(t *testing.T) {
	const ownerURL = mainAPI + "/owners/"

	// What the API refuses leaves the cell empty, and nothing else.
	t.Run("a related item the API does not hand out", func(t *testing.T) {
		var log bytes.Buffer
		var mu sync.Mutex
		shop := projecting(t, kitchen+"  plans: "+`{columns: [owner, owner.city, toys, visits, food], joins: [`+
			`{name: owner, column: ownerId, resource: owners, join: [name, vetId], nested: {name: vet, column: vetId, resource: vets, join: name}}, `+
			`{name: toys, column: toyIds, resource: toys, join: name}, `+
			`{name: visits, column: petId, resource: visits, join: reason}, `+
			`{name: food, column: foodId, resource: foods, join: [name], nested: {name: recipe, column: recipeId, resource: recipes, join: title}}]}`+"\n",
			func(doc *definition.Document, o *engine.Options) {
				doc.HTTP.JoinMaxWorkers, doc.HTTP.Retries = 1, 0
				o.Log = slog.New(slog.NewTextHandler(lockedWriter{&mu, &log}, &slog.HandlerOptions{Level: slog.LevelDebug}))
			})
		for path, response := range map[string]apitest.Response{
			adaPath:                       {Raw: adaJSON},
			foxPath:                       {Status: 500, Raw: "boom"},
			bobPath:                       {Status: 404},
			"/v1/owners/0f0f0f0f":         {Status: 403, Raw: `{"error":"not yours"}`},
			"/v1/owners?number=4711":      {Raw: `[]`},
			"/v1/toys/t-1":                {Raw: ballJSON},
			"/v1/toys/t-404":              {Status: 404},
			"/v1/toys/t-2":                {Raw: boneJSON},
			"/v1/pets/" + rex + "/visits": {Status: 500, Raw: "<html><body><h1>Error</h1>busy</body></html>"},
		} {
			shop.api.Handle("GET", path, response)
		}
		list := itemsOf(t, `[
			{"ownerId":"`+owner+`","toyIds":["t-1","t-404","t-2"],"petId":"`+rex+`"},
			{"ownerId":"`+bobID+`"},
			{"ownerId":"0f0f0f0f"},
			{"ownerId":"4711"},
			{"ownerId":".."}]`)
		table, err := shop.Project(t.Context(), "plans", list, everyJoin)
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		wantJSON(t, table.Rows, `[`+
			`[{"id":"`+owner+`","name":"Ada","vetId":"d-1"},"Bern",["Ball","Bone"],[],""],`+
			`["","","","",""],["","","","",""],["","","","",""],["","","","",""]]`)
		wantSent(t, shop.api,
			"GET "+ownerURL+owner, "GET "+mainAPI+"/vets/d-1",
			"GET "+mainAPI+"/toys/t-1", "GET "+mainAPI+"/toys/t-404", "GET "+mainAPI+"/toys/t-2",
			"GET "+adminAPI+"/pets/"+rex+"/visits",
			// A 404 is remembered, any other failure is asked for again.
			"GET "+ownerURL+bobID, "GET "+ownerURL+"0f0f0f0f", "GET "+ownerURL+"0f0f0f0f",
			"GET "+adminAPI+"/owners?number=4711")

		// Each failure is logged once for each lookup that met it, at
		// debug level: both columns of the owner ask for the four owners
		// that cannot be had, the vet is asked for by the first of them
		// alone.
		mu.Lock()
		logged := log.String()
		mu.Unlock()
		for part, times := range map[string]int{
			"level=DEBUG msg=join ":                                11,
			"msg=join resource=owners id=" + bobID + " ":           2,
			"msg=join resource=owners id=0f0f0f0f ":                2,
			"msg=join resource=owners id=4711 ":                    2,
			"msg=join resource=owners id=.. ":                      2,
			"msg=join resource=vets id=d-1 ":                       1,
			"msg=join resource=toys id=t-404 ":                     1,
			"msg=join resource=pets id=" + rex + " sub=visits ":    1,
			`error="failed to fetch vet by ID d-1: 500 boom"`:      1,
			`error="unable to find owner 4711"`:                    2,
			"msg=request method=GET url=" + ownerURL + owner + " ": 1,
		} {
			if got := strings.Count(logged, part); got != times {
				t.Errorf("the log holds %q %d times, want %d:\n%s", part, got, times, logged)
			}
		}
		if got, want := strings.Count(logged, "level="), strings.Count(logged, "level=DEBUG"); got != want {
			t.Errorf("the log holds %d lines, %d of them at debug level:\n%s", got, want, logged)
		}
	})

	fatal := map[string]struct {
		change func(doc *definition.Document, o *engine.Options)
		answer apitest.Response
		kind   apikit.Kind
		parts  []string
	}{
		"an API that cannot be reached": {
			answer: apitest.Response{Err: errors.New("connection refused")},
			kind:   apikit.KindFailed, parts: []string{"API request failed while trying to fetch owners", "connection refused"},
		},
		"an answer that is no JSON": {
			answer: apitest.Response{Raw: "<html>maintenance</html>"},
			kind:   apikit.KindFailed, parts: []string{"API request failed while trying to fetch owners", "the response is not JSON"},
		},
		"no credential": {
			change: func(_ *definition.Document, o *engine.Options) { o.Tokens = &tokens{} },
			answer: apitest.Response{Raw: adaJSON},
			kind:   apikit.KindUnauthenticated, parts: []string{"no API token available"},
		},
		"a credential the API rejects": {
			change: func(_ *definition.Document, o *engine.Options) { o.Tokens = &tokens{current: "t0ken"} },
			answer: apitest.Response{Status: 401},
			kind:   apikit.KindUnauthenticated, parts: []string{"the session has ended"},
		},
	}
	for name, test := range fatal {
		t.Run(name, func(t *testing.T) {
			var reports []engine.Progress
			shop := projecting(t, kitchen+"  plans: "+byOwner+"\n", func(doc *definition.Document, o *engine.Options) {
				doc.HTTP.JoinMaxWorkers, doc.HTTP.Retries = 1, 0
				o.Progress = func(p engine.Progress) { reports = append(reports, p) }
				if test.change != nil {
					test.change(doc, o)
				}
			})
			shop.api.Handle("GET", adaPath, test.answer)
			// What is left is not asked for: the fake API has no answer
			// for Bob.
			list := itemsOf(t, `[{"id":"p-0"},{"ownerId":"`+owner+`"},{"ownerId":"`+bobID+`"},{"ownerId":"`+owner+`"}]`)
			table, err := shop.Project(t.Context(), "plans", list, everyJoin)
			wantError(t, err, test.kind, test.parts...)
			if table != nil {
				t.Errorf("Project returned a table with its error")
			}
			if got := len(shop.api.Requests()); got > 1 {
				t.Errorf("%d requests were sent, want at most 1", got)
			}
			// The row before the failure is all that is complete.
			if want := []engine.Progress{{Stage: engine.StageJoin, Done: 0, Total: 4}, {Stage: engine.StageJoin, Done: 1, Total: 4}}; !slices.Equal(reports, want) {
				t.Errorf("progress: %+v, want %+v", reports, want)
			}
			// The items are completed only when all is well.
			wantJSON(t, list, `[{"id":"p-0"},{"ownerId":"`+owner+`"},{"ownerId":"`+bobID+`"},{"ownerId":"`+owner+`"}]`)
		})
	}

	// A nested join fails like the join it belongs to: when it is added
	// to the related item, when a path leads into it, and when it is
	// among the items listed for the related one.
	nested := map[string]struct {
		plans   string
		failing string
	}{
		"a nested join that cannot be reached": {
			plans: `{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, join: [name, vetId], ` +
				`nested: {name: vet, column: vetId, resource: vets, join: name}}]}`,
			failing: foxPath,
		},
		"a path into one": {
			plans: `{columns: [owner.vet], joins: [{name: owner, column: ownerId, resource: owners, join: name, ` +
				`nested: {name: vet, column: vetId, resource: vets, join: name}}]}`,
			failing: foxPath,
		},
		"one that is listed": {
			plans: `{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, join: [name, vetId], ` +
				`nested: {name: bill, column: vetId, resource: bills, join: total}}]}`,
			failing: adaPath + "/bills",
		},
	}
	for name, test := range nested {
		t.Run(name, func(t *testing.T) {
			shop := projecting(t, kitchen+"  bills: {parent: owners}\n  plans: "+test.plans+"\n", func(doc *definition.Document, _ *engine.Options) {
				doc.HTTP.JoinMaxWorkers, doc.HTTP.Retries = 1, 0
			})
			shop.api.Handle("GET", adaPath, apitest.Response{Raw: adaJSON})
			shop.api.Handle("GET", test.failing, apitest.Response{Err: errors.New("connection refused")})
			_, err := shop.Project(t.Context(), "plans", itemsOf(t, `[{"ownerId":"`+owner+`"}]`), everyJoin)
			wantError(t, err, apikit.KindFailed, "API request failed while trying to fetch", "connection refused")
		})
	}

	t.Run("a run that has ended before it began", func(t *testing.T) {
		shop := joining(t, byOwner, nil)
		ctx, interrupt := context.WithCancel(t.Context())
		interrupt()
		_, err := shop.Project(ctx, "plans", itemsOf(t, `[{"ownerId":"`+owner+`"}]`), everyJoin)
		if !errors.Is(err, context.Canceled) || apikit.KindOf(err) != apikit.KindCanceled {
			t.Errorf("Project failed with %v (kind %q), want context.Canceled", err, apikit.KindOf(err))
		}
		// Without joins there is nothing to give up.
		table, err := shop.Project(ctx, "plans", itemsOf(t, `[{"id":"p-1"}]`), engine.ProjectOptions{})
		if err != nil {
			t.Fatalf("Project: %v", err)
		}
		wantJSON(t, table.Rows, `[[""]]`)
		wantSent(t, shop.api)
	})
}

func TestJoinStopsWhenTheRunEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		shop := projecting(t, kitchen+"  plans: "+byOwner+"\n", func(doc *definition.Document, _ *engine.Options) {
			doc.HTTP.JoinMaxWorkers = 2
		})
		never := make(chan struct{})
		for _, id := range []string{owner, bobID, kittyID} {
			shop.api.Handle("GET", "/v1/owners/"+id, apitest.Response{Raw: adaJSON, Until: never})
		}
		list := itemsOf(t, `[{"ownerId":"`+owner+`"},{"ownerId":"`+bobID+`"},{"ownerId":"`+kittyID+`"}]`)
		ctx, interrupt := context.WithCancel(t.Context())
		var table *engine.Table
		var err error
		var done sync.WaitGroup
		done.Go(func() { table, err = shop.Project(ctx, "plans", list, everyJoin) })

		synctest.Wait()
		interrupt()
		// The requests under way are given up, or this would wait forever.
		done.Wait()
		if !errors.Is(err, context.Canceled) || apikit.KindOf(err) != apikit.KindCanceled {
			t.Errorf("Project failed with %v (kind %q), want context.Canceled", err, apikit.KindOf(err))
		}
		if table != nil {
			t.Errorf("Project returned a table with its error")
		}
		// The third owner is never asked for.
		if got := len(shop.api.Requests()); got != 2 {
			t.Errorf("%d requests were sent, want 2", got)
		}
	})
}

func TestJoinsRunSideBySide(t *testing.T) {
	// held scripts the owners with the given IDs, each held back until its
	// channel is closed.
	held := func(api *apitest.Server, ids ...string) map[string]chan struct{} {
		gates := map[string]chan struct{}{}
		for _, id := range ids {
			gates[id] = make(chan struct{})
			api.Handle("GET", "/v1/owners/"+id, apitest.Response{Raw: `{"id":"` + id + `","name":"owner ` + id + `","vetId":"d-1"}`, Until: gates[id]})
		}
		return gates
	}
	// asked returns the IDs of the owners asked for so far, sorted:
	// requests that are sent at the same time arrive in any order.
	asked := func(api *apitest.Server) string {
		var ids []string
		for _, request := range api.Requests() {
			ids = append(ids, request.Path[strings.LastIndex(request.Path, "/")+1:])
		}
		slices.Sort(ids)
		return strings.Join(ids, " ")
	}

	t.Run("as many at a time as the definition allows", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var mu sync.Mutex
			var reports []engine.Progress
			shop := projecting(t, kitchen+"  plans: "+byOwner+"\n", func(doc *definition.Document, o *engine.Options) {
				doc.HTTP.JoinMaxWorkers = 2
				o.Progress = func(p engine.Progress) {
					mu.Lock()
					defer mu.Unlock()
					reports = append(reports, p)
				}
			})
			gates := held(shop.api, "a1", "a2", "a3", "a4")
			list := itemsOf(t, `[{"ownerId":"a1"},{"id":"no owner"},{"ownerId":"a2"},{"ownerId":"a3"},{"ownerId":"a4"},{"ownerId":"a1"}]`)
			var table *engine.Table
			var err error
			var done sync.WaitGroup
			done.Go(func() { table, err = shop.Project(t.Context(), "plans", list, everyJoin) })
			// progress checks which rows were reported complete so far.
			progress := func(want ...int) {
				t.Helper()
				mu.Lock()
				defer mu.Unlock()
				var got []int
				for _, report := range reports {
					if report.Stage != engine.StageJoin || report.Total != 6 {
						t.Errorf("report %+v, want a join of 6 rows", report)
					}
					got = append(got, report.Done)
				}
				if !slices.Equal(got, want) {
					t.Errorf("rows reported complete: %v, want %v", got, want)
				}
			}

			// Two workers, two requests. The row without an owner is
			// complete as soon as a worker has looked at it.
			synctest.Wait()
			if got := asked(shop.api); got != "a1 a2" {
				t.Errorf("asked for %s, want a1 a2", got)
			}
			progress(0, 1)

			// A worker that is done takes the next lookup.
			close(gates["a2"])
			synctest.Wait()
			if got := asked(shop.api); got != "a1 a2 a3" {
				t.Errorf("asked for %s, want a1 a2 a3", got)
			}
			progress(0, 1, 2)

			close(gates["a3"])
			synctest.Wait()
			if got := asked(shop.api); got != "a1 a2 a3 a4" {
				t.Errorf("asked for %s, want a1 a2 a3 a4", got)
			}
			progress(0, 1, 2, 3)

			// The last row asks for the first owner once more, and waits
			// for the request that is under way.
			close(gates["a4"])
			synctest.Wait()
			progress(0, 1, 2, 3, 4)

			close(gates["a1"])
			done.Wait()
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			wantJSON(t, table.Rows, `[["owner a1"],[""],["owner a2"],["owner a3"],["owner a4"],["owner a1"]]`)
			if got := asked(shop.api); got != "a1 a2 a3 a4" {
				t.Errorf("asked for %s, want a1 a2 a3 a4", got)
			}
			progress(0, 1, 2, 3, 4, 5, 6)
		})
	})

	// A lookup asks for its nested joins itself, so it needs no second
	// worker to get on.
	t.Run("nested joins need no worker of their own", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			shop := projecting(t, kitchen+"  plans: "+`{columns: [owner], joins: [{name: owner, column: ownerId, resource: owners, `+
				`join: [name, vetId], nested: {name: vet, column: vetId, resource: vets, join: name}}]}`+"\n",
				func(doc *definition.Document, _ *engine.Options) { doc.HTTP.JoinMaxWorkers = 1 })
			gates := held(shop.api, "a1", "a2")
			shop.api.Handle("GET", foxPath, apitest.Response{Raw: foxJSON})
			var table *engine.Table
			var err error
			var done sync.WaitGroup
			done.Go(func() {
				table, err = shop.Project(t.Context(), "plans", itemsOf(t, `[{"ownerId":"a1"},{"ownerId":"a2"}]`), everyJoin)
			})
			synctest.Wait()
			close(gates["a1"])
			synctest.Wait()
			if got := asked(shop.api); got != "a1 a2 d-1" {
				t.Errorf("asked for %s, want a1 a2 d-1", got)
			}
			close(gates["a2"])
			done.Wait()
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			wantJSON(t, table.Rows, `[[{"id":"a1","name":"owner a1","vetId":"d-1","vet":"Dr. Fox"}],[{"id":"a2","name":"owner a2","vetId":"d-1","vet":"Dr. Fox"}]]`)
		})
	})

	// What the paths of a column find is put together in the order of
	// the paths, not in that of the answers.
	t.Run("the paths of a column keep their order", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			shop := projecting(t, kitchen+"  plans: "+`{columns: [{label: who, paths: [first, note, second]}], joins: [`+
				`{name: first, column: firstId, resource: owners, join: name}, {name: second, column: secondId, resource: owners, join: name}]}`+"\n", nil)
			gates := held(shop.api, "a1", "a2")
			var table *engine.Table
			var err error
			var done sync.WaitGroup
			done.Go(func() {
				table, err = shop.Project(t.Context(), "plans", itemsOf(t, `[{"firstId":"a1","secondId":"a2","note":"and"}]`), everyJoin)
			})
			synctest.Wait()
			if got := asked(shop.api); got != "a1 a2" {
				t.Errorf("asked for %s, want a1 a2", got)
			}
			close(gates["a2"])
			synctest.Wait()
			close(gates["a1"])
			done.Wait()
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			wantJSON(t, table.Rows, `[["owner a1, and, owner a2"]]`)
		})
	})
}

// Many rows with several joins each, as fast as the workers go: the race
// detector watches.
func TestJoinManyRows(t *testing.T) {
	const rows, owners = 300, 7
	var mu sync.Mutex
	var reports []engine.Progress
	shop := projecting(t, kitchen+"  plans: "+`{columns: [owner, {label: who, paths: [owner.city, food.recipe.title]}, food, visits], joins: [`+
		`{name: owner, column: ownerId, resource: owners, join: name}, `+
		`{name: food, column: foodId, resource: foods, join: [name, maker], nested: {name: recipe, column: recipeId, resource: recipes, join: [id, title]}}, `+
		`{name: visits, column: petId, resource: visits, join: reason, sort: [{column: reason}]}]}`+"\n",
		func(doc *definition.Document, o *engine.Options) {
			doc.HTTP.JoinMaxWorkers = 8
			o.Progress = func(p engine.Progress) {
				mu.Lock()
				defer mu.Unlock()
				reports = append(reports, p)
			}
		})
	for i := range owners {
		shop.api.Handle("GET", fmt.Sprintf("/v1/owners/o-%d", i), apitest.Response{Raw: fmt.Sprintf(`{"id":"o-%d","name":"owner %d","city":"city %d"}`, i, i, i)})
	}
	shop.api.Handle("GET", kibblePath, apitest.Response{Raw: kibbleJSON})
	shop.api.Handle("GET", recipesPath, apitest.Response{Raw: recipesJSON})
	shop.api.Handle("GET", "/v1/pets/"+rex+"/visits", apitest.Response{Raw: `[{"id":"d-2","reason":"shot"},{"id":"d-1","reason":"checkup"}]`})

	var list, want []string
	for i := range rows {
		list = append(list, fmt.Sprintf(`{"ownerId":"o-%d","foodId":"f-1","recipeId":"r-%d","petId":"%s"}`, i%owners, 1+i%2, rex))
		title := []string{"Classic", "Green"}[i%2]
		want = append(want, fmt.Sprintf(`["owner %d","city %d, %s",{"id":"f-1","name":"Kibble","maker":"Bones","recipe":{"id":"r-%d","title":"%s"}},["checkup","shot"]]`,
			i%owners, i%owners, title, 1+i%2, title))
	}
	plans := itemsOf(t, "["+strings.Join(list, ",")+"]")
	table, err := shop.Project(t.Context(), "plans", plans, everyJoin)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	wantJSON(t, table.Rows, "["+strings.Join(want, ",")+"]")
	// Requests for one URL at the same time share one answer, and later
	// ones are answered from memory.
	if got := len(shop.api.Requests()); got != owners+3 {
		t.Errorf("%d requests were sent, want %d", got, owners+3)
	}
	if len(reports) != rows+1 {
		t.Fatalf("%d reports, want %d", len(reports), rows+1)
	}
	for i, report := range reports {
		if want := (engine.Progress{Stage: engine.StageJoin, Done: i, Total: rows}); report != want {
			t.Fatalf("report %d is %+v, want %+v", i, report, want)
		}
	}
}

func TestCompareSortValues(t *testing.T) {
	tests := map[string]struct {
		a, b string
		want int
	}{
		"nothing comes last":                 {`null`, `"z"`, 1},
		"whatever it is compared with":       {`0`, `null`, -1},
		"nothing equals nothing":             {`null`, `null`, 0},
		"numbers by their value":             {`2`, `10`, -1},
		"with fractions":                     {`2.5`, `2`, 1},
		"however they are spelled":           {`1.0`, `1`, 0},
		"and however large":                  {`9007199254740993`, `9007199254740992`, 1},
		"false before true":                  {`false`, `true`, -1},
		"true is one":                        {`true`, `1`, 0},
		"false is less than a half":          {`false`, `0.5`, -1},
		"digits in a text count as a number": {`"v9"`, `"v10"`, -1},
		"in every place":                     {`"4.0.10"`, `"4.0.9"`, 1},
		"leading zeros do not count":         {`"v007"`, `"v7"`, 0},
		"numbers beyond any integer":         {`"v100000000000000000000000001"`, `"v100000000000000000000000000"`, 1},
		"letters without regard to case":     {`"Rex"`, `"rex"`, 0},
		"in their order":                     {`"Bello"`, `"adaJSON"`, 1},
		"digits before letters":              {`"1"`, `"a"`, -1},
		"a text before one it begins":        {`"3"`, `"3a"`, -1},
		"an empty text first":                {`""`, `"0"`, -1},
		"a number and a text as texts":       {`3`, `"3a"`, -1},
		"a number as it is spelled":          {`2.50`, `"2.5"`, 1},
		"a boolean among texts is a number":  {`true`, `"1"`, 0},
		"other values by their text":         {`["a"]`, `["b"]`, -1},
		"letters beyond ASCII":               {`"Ärger"`, `"ärger"`, 0},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			a, b := decode(t, test.a), decode(t, test.b)
			if got := engine.CompareSortValues(a, b); got != test.want {
				t.Errorf("CompareSortValues(%s, %s) = %d, want %d", test.a, test.b, got, test.want)
			}
			if got := engine.CompareSortValues(b, a); got != -test.want {
				t.Errorf("CompareSortValues(%s, %s) = %d, want %d", test.b, test.a, got, -test.want)
			}
		})
	}

	t.Run("a list of all kinds", func(t *testing.T) {
		values := itemsOf(t, `["v10","v9","V2",null,3,"3a",true,2.5,"","abc"]`)
		slices.SortStableFunc(values, engine.CompareSortValues)
		wantJSON(t, values, `["",true,2.5,3,"3a","abc","V2","v9","v10",null]`)
	})
}
