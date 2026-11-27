package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mittwald/cli-kit/secret"
	"github.com/spf13/cobra"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/apitest"
	"github.com/mittwald/api-kit/cli"
	"github.com/mittwald/api-kit/definition"
	"github.com/mittwald/api-kit/engine"
)

// snackAPI is the base URL of the snack bar.
const snackAPI = "https://snacks.example.test/api"

// snackBar is the definition of an invented snack bar whose API wraps its
// answers in objects, numbers nothing itself and hands out a new token with
// its answers: what the definition features for such an API are for.
const snackBar = `
environments:
  prod:
    apis: {main: "https://snacks.example.test/api"}
pagination: {limitParam: ""}
auth:
  header: {name: Authorization, value: "Bearer ${token}"}
  sources: [store]
  renew:
    session: {token: jwt}
  login:
    methods:
      password:
        kind: password
        prompts:
          - {name: employeeId, label: Employee ID}
          - {name: password, label: Password, secret: true}
        request: {method: POST, path: auth/login, body: {employeeId: "${employeeId}", password: "${password}"}}
        session: {token: jwt}
output:
  omit: [jwt]
  tableStyle: light
resources:
  articles:
    responseField: articles
    query: {includeDisabled: "False"}
    filters: ["category="]
    columns:
      - articleId
      - {label: type, paths: [category], values: {drink: "🥤", snack: "🍫"}, align: center}
      - {label: price, paths: [centPrice], format: cents, align: right}
      - {label: name, paths: [name], style: bold}
  menus:
    responseField: menus
    sort: [{column: date}, {column: variant}]
    number: menuNumber
    columns:
      - menuNumber
      - variant
      - {label: booked, paths: [booked], values: {"true": "yes", "false": "no"}}
    joins:
      - {name: booked, column: menuId, resource: bookings, by: menuId, exists: true}
  bookings:
    apiResource: menus/booked
    responseField: bookings
    columns: [bookingId, menuId]
    setters:
      - name: book
        requiresId: false
        method: POST
        pathTemplate: menus/book
        payloadTemplate: {menuId: "${menu}"}
        parameters:
          - name: menu
            positional: true
            help: Number or ID of the menu
            lookup: {get: menus, match: [menuId, menuNumber], select: menuId}
  orders:
    responseField: orders
    columns: [orderId, article, amount]
    setters:
      - name: order
        requiresId: false
        method: POST
        pathTemplate: orders
        responseField: order
        payloadTemplate: '{"articleId": "${article}", "amount": ${amount}, "orderDate": "${date}"}'
        parameters:
          - {name: article, positional: true, help: Article to order}
          - name: date
            type: time
            required: false
            default: now
            timezone: Europe/Berlin
            layouts: ["2.1.2006", "2.1.2006 15:4"]
            dayTime: "12:00"
        stepParameters:
          - {name: amount, type: int, default: 1}
  stats:
    apiResource: user/limit
    include:
      - {resource: month-stats, fields: {currentMonth: totalSpend}}
    columns: [employeeId, orderLimit, currentMonth]
  month-stats:
    apiResource: user/stats/month
`

// snackTool is a tool for the snack bar, wired to a fake API, with the
// format of prices in cents.
func snackTool(t *testing.T) *tool {
	t.Helper()
	h := newTool(t)
	h.options.Definition = definition.Source{Name: "snacks.yaml", Data: []byte(snackBar)}
	h.options.CellFormats = map[string]func(any) string{
		"cents": func(value any) string {
			number, ok := value.(json.Number)
			if !ok {
				return apikit.Text(value)
			}
			cents, _ := number.Int64()
			return fmt.Sprintf("%.2f €", float64(cents)/100)
		},
	}
	return h
}

const articleList = `{"articles": [
  {"articleId": "cola", "name": "Cola", "category": "drink", "centPrice": 150},
  {"articleId": "kitkat", "name": "KitKat", "category": "snack", "centPrice": 80}
]}`

func TestListingsInAField(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/articles?includeDisabled=False", apitest.Response{Body: raw(articleList)})
	stdout, stderr, code := h.run("get", "articles", "--color=never")
	if code != apikit.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	want := "" +
		"┌───────────┬──────┬────────┬────────┐\n" +
		"│ ARTICLEID │ TYPE │  PRICE │ NAME   │\n" +
		"├───────────┼──────┼────────┼────────┤\n" +
		"│ cola      │  🥤  │ 1.50 € │ Cola   │\n" +
		"├───────────┼──────┼────────┼────────┤\n" +
		"│ kitkat    │  🍫  │ 0.80 € │ KitKat │\n" +
		"└───────────┴──────┴────────┴────────┘\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}

	// A filter of the same key replaces a fixed query parameter.
	h.api.Handle("GET", snackAPI+"/articles?category=drink&includeDisabled=True", apitest.Response{Body: raw(articleList)})
	if _, stderr, code := h.run("get", "articles", "-f", "category=drink,includeDisabled=True", "-o", "id"); code != apikit.ExitOK {
		t.Errorf("exit code %d, stderr %q", code, stderr)
	}
}

func TestColumnStylesAreDrawnInColor(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/articles?includeDisabled=False", apitest.Response{Body: raw(articleList)})
	stdout, _, _ := h.run("get", "articles", "--color=always")
	if !strings.Contains(stdout, "\x1b[1mCola\x1b[0m") {
		t.Errorf("the name is not bold:\n%q", stdout)
	}
}

const menuList = `{"menus": [
  {"menuId": "m3", "variant": "veggie", "date": "2026-07-16"},
  {"menuId": "m1", "variant": "meat", "date": "2026-07-15"},
  {"menuId": "m2", "variant": "veggie", "date": "2026-07-15"}
]}`

func TestListingsAreSortedNumberedAndJoinedByField(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/menus", apitest.Response{Body: raw(menuList)})
	h.api.Handle("GET", snackAPI+"/menus/booked", apitest.Response{Body: raw(`{"bookings": [{"bookingId": "b1", "menuId": "m2"}]}`)})
	stdout, stderr, code := h.run("get", "menus", "-o", "csv")
	if code != apikit.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	want := "menuNumber,variant,booked\n1,meat,no\n2,veggie,yes\n3,veggie,no\n"
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	// The listing of bookings is fetched once for all menus.
	if n := len(h.api.Requests()); n != 2 {
		t.Errorf("%d requests, want 2", n)
	}
}

func TestPositionalParameterWithLookupByNumber(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/menus", apitest.Response{Body: raw(menuList)})
	h.api.Handle("POST", snackAPI+"/menus/book", apitest.Response{Body: raw(`{"bookingId": "b2", "menuId": "m2", "jwt": "fresh"}`)})
	stdout, stderr, code := h.run("book", "bookings", "2", "-o", "json")
	if code != apikit.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	requests := h.api.Requests()
	if body, _ := apikit.Marshal(requests[len(requests)-1].Body); string(body) != `{"menuId":"m2"}` {
		t.Errorf("payload = %s", body)
	}
	// The token of the answer is no business of the output.
	if strings.Contains(stdout, "jwt") || !strings.Contains(stdout, `"bookingId": "b2"`) {
		t.Errorf("stdout:\n%s", stdout)
	}

	_, stderr, code = h.run("book", "bookings", "2", "--menu", "1")
	if code != apikit.ExitUsage || !strings.Contains(stderr, "cannot be given as well") {
		t.Errorf("both the argument and the option: exit code %d, stderr %q", code, stderr)
	}
}

func TestDefaultsLayoutsAndTheTimeOfADate(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone data")
	}
	tomorrow := time.Now().In(berlin).AddDate(0, 0, 1)
	tests := []struct {
		args []string
		want string // the start of orderDate, "" for now
	}{
		{[]string{"cola"}, ""},
		{[]string{"cola", "--date", tomorrow.Format("02.01.2006")}, tomorrow.Format("2006-01-02") + "T12:00:00"},
		{[]string{"cola", "--date", tomorrow.Format("2006-01-02")}, tomorrow.Format("2006-01-02") + "T12:00:00"},
		{[]string{"cola", "--date", tomorrow.Format("2.1.2006") + " 9:30"}, tomorrow.Format("2006-01-02") + "T09:30:00"},
		{[]string{"cola", "--date", time.Now().In(berlin).Format("2.1.2006")}, ""},
		{[]string{"cola", "--date", "tomorrow"}, tomorrow.Format("2006-01-02") + "T12:00:00"},
		{[]string{"cola", "--date", "+1d"}, tomorrow.Format("2006-01-02") + "T12:00:00"},
		{[]string{"cola", "--date", "today"}, ""},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			h := snackTool(t)
			h.api.Handle("POST", snackAPI+"/orders", apitest.Response{Body: raw(`{"order": {"orderId": "o1", "article": "Cola", "amount": 1}}`)})
			before := time.Now()
			stdout, stderr, code := h.run(append([]string{"order", "orders"}, tt.args...)...)
			if code != apikit.ExitOK {
				t.Fatalf("exit code %d, stderr %q", code, stderr)
			}
			var payload struct {
				ArticleID string          `json:"articleId"`
				Amount    json.RawMessage `json:"amount"`
				OrderDate string          `json:"orderDate"`
			}
			requests := h.api.Requests()
			body, _ := apikit.Marshal(requests[len(requests)-1].Body)
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.ArticleID != "cola" || string(payload.Amount) != "1" {
				t.Errorf("payload = %+v", payload)
			}
			date, err := time.Parse(time.RFC3339Nano, payload.OrderDate)
			switch {
			case err != nil:
				t.Errorf("orderDate %q: %v", payload.OrderDate, err)
			case tt.want == "" && (date.Before(before.Add(-time.Second)) || date.After(time.Now().Add(time.Second))):
				t.Errorf("orderDate = %s, want now", payload.OrderDate)
			case tt.want != "" && !strings.HasPrefix(payload.OrderDate, tt.want):
				t.Errorf("orderDate = %s, want %s…", payload.OrderDate, tt.want)
			}
			// The order is shown out of the field of the answer.
			if !strings.Contains(stdout, "o1") {
				t.Errorf("stdout:\n%s", stdout)
			}
		})
	}
}

func TestIncludeAddsFieldsOfOtherAnswers(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/user/limit", apitest.Response{Body: raw(`{"employeeId": "e1", "orderLimit": 50}`)})
	h.api.Handle("GET", snackAPI+"/user/stats/month", apitest.Response{Body: raw(`{"totalSpend": 1234}`)})
	stdout, stderr, code := h.run("get", "stats", "-o", "csv")
	if code != apikit.ExitOK || stdout != "employeeId,orderLimit,currentMonth\ne1,50,1234\n" {
		t.Errorf("exit code %d, stderr %q, stdout:\n%s", code, stderr, stdout)
	}
}

func TestAnswersRenewTheStoredSession(t *testing.T) {
	h := snackTool(t)
	store := secret.NewMemory(map[string]string{"prod": "old"})
	h.options.Store = store
	h.api.Handle("GET", snackAPI+"/menus", apitest.Response{Body: raw(menuList)})
	h.api.Handle("POST", snackAPI+"/menus/book", apitest.Response{Body: raw(`{"bookingId": "b2", "jwt": "fresh"}`)})
	h.api.Handle("GET", snackAPI+"/menus/booked", apitest.Response{Body: raw(`{"bookings": []}`)})
	if _, stderr, code := h.runBare("book", "bookings", "--menu", "m2", "-o", "json"); code != apikit.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	if stored, _ := store.Get("prod"); !strings.Contains(stored, "fresh") {
		t.Errorf("stored session = %q, want the renewed token", stored)
	}
	// The next request of the same run would carry it; the next run does.
	if _, stderr, code := h.runBare("get", "bookings"); code != apikit.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	requests := h.api.Requests()
	if got := requests[len(requests)-1].Header.Get("Authorization"); got != "Bearer fresh" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestCallResultsShowWhatTheHostWants(t *testing.T) {
	h := snackTool(t)
	h.options.CallResults = map[string]func(*cli.Env, *cobra.Command, *engine.CallResult) error{
		"book bookings": func(env *cli.Env, cmd *cobra.Command, result *engine.CallResult) error {
			api, err := env.Engine()
			if err != nil {
				return err
			}
			listing, err := api.Get(cmd.Context(), engine.GetRequest{Resource: "bookings"})
			if err != nil {
				return err
			}
			return env.Render(cmd, listing)
		},
	}
	h.api.Handle("GET", snackAPI+"/menus", apitest.Response{Body: raw(menuList)})
	h.api.Handle("POST", snackAPI+"/menus/book", apitest.Response{Body: raw(`{"bookingId": "b2", "menuId": "m2"}`)})
	h.api.Handle("GET", snackAPI+"/menus/booked", apitest.Response{Body: raw(`{"bookings": [{"bookingId": "b1", "menuId": "m1"}, {"bookingId": "b2", "menuId": "m2"}]}`)})
	stdout, stderr, code := h.run("book", "bookings", "--menu", "m2", "-o", "id")
	if code != apikit.ExitOK || stdout != "b1\nb2\n" {
		t.Errorf("exit code %d, stderr %q, stdout %q", code, stderr, stdout)
	}
}

func TestDefinitionMistakesOfTheNewKeys(t *testing.T) {
	for _, tt := range []struct{ name, yaml, want string }{
		{"unknown format", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {columns: [{paths: [x], format: money}]}}}`, `unknown format "money"`},
		{"alignment", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {columns: [{paths: [x], align: middle}]}}}`, "must be left, right or center"},
		{"table style", `{environments: {prod: {apis: {main: "https://x.test"}}}, output: {tableStyle: wavy}}`, `unknown table style "wavy"`},
		{"two positional", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {setters: [{name: go, requiresId: false, parameters: [{name: x, positional: true}, {name: y, positional: true}]}]}}}`, "only one parameter can be positional"},
		{"two positional setters", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {setters: [{name: go, requiresId: false, parameters: [{name: x, positional: true}]}, {name: go, requiresId: false, parameters: [{name: y, positional: true}]}]}}}`, "only one parameter of the go setters"},
		{"positional with ID", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {setters: [{name: go, parameters: [{name: x, positional: true}]}]}}}`, "takes the ID of an item"},
		{"default of a required one", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {setters: [{name: go, parameters: [{name: x, default: y}]}]}}}`, "has no default"},
		{"include cycle", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {include: [{resource: b, fields: {x: y}}]}, b: {include: [{resource: a, fields: {x: y}}]}}}`, "fetched without end"},
		{"day time", `{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {setters: [{name: go, parameters: [{name: x, type: time, dayTime: noon}]}]}}}`, "must be a time of day"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := snackTool(t)
			h.options.Definition = definition.Source{Name: "bad.yaml", Data: []byte(tt.yaml)}
			_, stderr, code := h.run("get", "a")
			if code == apikit.ExitOK || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit code %d, stderr:\n%s\nwant %q", code, stderr, tt.want)
			}
		})
	}
}

func TestLoginReadsPipedAnswersAndHostsLogIn(t *testing.T) {
	h := snackTool(t)
	store := secret.NewMemory(nil)
	h.options.Store = store
	var format string
	h.options.Commands = []cli.CommandFactory{func(env *cli.Env) *cobra.Command {
		return &cobra.Command{Use: "login", RunE: func(cmd *cobra.Command, _ []string) error {
			if err := env.Login(cmd.Context(), ""); err != nil {
				return err
			}
			format, _ = env.OutputFormat()
			return nil
		}}
	}}
	h.api.Handle("POST", snackAPI+"/auth/login", apitest.Response{Body: raw(`{"jwt": "t0k3n", "employeeId": "e1"}`)})
	var out, errOut strings.Builder
	options := h.options
	options.Streams = cli.Streams{In: strings.NewReader("e1\nhunter2\n"), Out: &out, Err: &errOut}
	if code := cli.New(options).Execute(t.Context(), []string{"login", "-o", "yaml"}); code != apikit.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
	requests := h.api.Requests()
	if body, _ := apikit.Marshal(requests[0].Body); string(body) != `{"employeeId":"e1","password":"hunter2"}` {
		t.Errorf("login body = %s", body)
	}
	if stored, _ := store.Get("prod"); !strings.Contains(stored, "t0k3n") {
		t.Errorf("stored session = %q", stored)
	}
	if format != "yaml" {
		t.Errorf("OutputFormat = %q", format)
	}
}

func TestTimesAreSentInTheirLayout(t *testing.T) {
	h := snackTool(t)
	h.options.Definition = definition.Source{Name: "snacks.yaml", Data: []byte(strings.Replace(snackBar,
		`dayTime: "12:00"`, `dayTime: "12:00"`+"\n            sendLayout: \"2006-01-02T15:04:05Z\"", 1))}
	h.api.Handle("POST", snackAPI+"/orders", apitest.Response{Body: raw(`{"order": {"orderId": "o1"}}`)})
	if _, stderr, code := h.run("order", "orders", "cola", "--date", "2030-01-02 08:15"); code != apikit.ExitOK {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	body, _ := apikit.Marshal(h.api.Requests()[0].Body)
	if !strings.Contains(string(body), `"orderDate":"2030-01-02T08:15:00Z"`) {
		t.Errorf("payload = %s", body)
	}
}

func TestWideKeepsColumnFormatsAndOmitReachesJoins(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/articles?includeDisabled=False", apitest.Response{Body: raw(articleList)})
	stdout, stderr, code := h.run("get", "articles", "-o", "wide", "--color=never")
	if code != apikit.ExitOK || !strings.Contains(stdout, "1.50 €") || !strings.Contains(stdout, "🥤") {
		t.Errorf("exit code %d, stderr %q, stdout:\n%s", code, stderr, stdout)
	}

	h = snackTool(t)
	h.api.Handle("GET", snackAPI+"/menus", apitest.Response{Body: raw(menuList)})
	h.api.Handle("GET", snackAPI+"/menus/booked", apitest.Response{Body: raw(`{"bookings": [{"bookingId": "b1", "menuId": "m2", "jwt": "secret"}]}`)})
	overlay := h.overlay("resources:\n  menus:\n    joins:\n      - {name: booking, column: menuId, resource: bookings, by: menuId, join: [bookingId, jwt]}\n")
	_ = overlay
	stdout, stderr, code = h.run("get", "menus", "-o", "json")
	if code != apikit.ExitOK || strings.Contains(stdout, "secret") || !strings.Contains(stdout, `"booking": "b1"`) {
		t.Errorf("exit code %d, stderr %q, stdout:\n%s", code, stderr, stdout)
	}
}

func TestDefaultsAreCheckedAndTyped(t *testing.T) {
	h := snackTool(t)
	h.options.Definition = definition.Source{Name: "bad.yaml", Data: []byte(`{environments: {prod: {apis: {main: "https://x.test"}}}, resources: {a: {setters: [{name: go, requiresId: false, parameters: [{name: x, required: false, choices: [a, b], default: c}, {name: n, type: int, required: false, default: many}]}]}}}`)}
	_, stderr, code := h.run("get", "a")
	if code == apikit.ExitOK || !strings.Contains(stderr, "none of the choices") || !strings.Contains(stderr, "whole number") {
		t.Errorf("exit code %d, stderr:\n%s", code, stderr)
	}
}

func TestOmittedFieldsStayOutOfTableCells(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/menus", apitest.Response{Body: raw(menuList)})
	h.api.Handle("GET", snackAPI+"/menus/booked", apitest.Response{Body: raw(`{"bookings": [{"bookingId": "b1", "menuId": "m2", "jwt": "secret"}]}`)})
	h.overlay("resources:\n  menus:\n    columns: [menuNumber, {label: token, paths: [booking.jwt]}]\n    joins:\n      - {name: booking, column: menuId, resource: bookings, by: menuId, join: [bookingId, jwt]}\n")
	stdout, stderr, code := h.run("get", "menus", "-o", "csv", "--config-override")
	if code != apikit.ExitOK || strings.Contains(stdout, "secret") {
		t.Errorf("exit code %d, stderr %q, stdout:\n%s", code, stderr, stdout)
	}
}

func TestExistsOfAJoinByID(t *testing.T) {
	h := snackTool(t)
	h.api.Handle("GET", snackAPI+"/orders", apitest.Response{Body: raw(`{"orders": [{"orderId": "o1", "articleId": "cola"}, {"orderId": "o2", "articleId": "gone"}]}`)})
	h.api.Handle("GET", snackAPI+"/articles/cola", apitest.Response{Body: raw(`{"articleId": "cola"}`)})
	h.api.Handle("GET", snackAPI+"/articles/gone", apitest.Response{Status: 404, Body: raw(`{}`)})
	h.overlay("resources:\n  orders:\n    columns: [orderId, {label: known, paths: [article]}]\n    joins:\n      - {name: article, column: articleId, resource: articles, exists: true}\n")
	stdout, stderr, code := h.run("get", "orders", "-o", "csv", "--config-override")
	if code != apikit.ExitOK || stdout != "orderId,known\no1,true\no2,false\n" {
		t.Errorf("exit code %d, stderr %q, stdout:\n%s", code, stderr, stdout)
	}
}
