package engine

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	apikit "github.com/mittwald/api-kit"
	"github.com/mittwald/api-kit/definition"
)

// Doer sends HTTP requests. An [*http.Client] is one.
type Doer interface {
	// Do sends a request and returns the response, as [http.Client.Do]
	// does.
	Do(*http.Request) (*http.Response, error)
}

// TokenSource supplies the credential the requests carry. Its methods are
// called from several goroutines at once.
type TokenSource interface {
	// Token returns the token to send. It is asked for before every
	// request and should answer from memory: whether the token is still
	// good shows when the API answers. An error ends the request and is
	// returned to the caller of the Engine as it is.
	Token(ctx context.Context) (string, error)
	// Rejected is called when the API answered a request carrying token
	// with status 401. It returns a token to send the request with once
	// more, or an error when there is none, which is returned to the
	// caller of the Engine as it is.
	Rejected(ctx context.Context, token string) (string, error)
	// Confirmed is called when a request carrying token succeeded.
	Confirmed(token string)
}

// Options configure an [Engine].
type Options struct {
	// Definition describes the API. It is required and must not change
	// while the Engine is in use.
	Definition *definition.Document
	// Environment names the environment whose APIs the requests go to.
	// Empty means the first one of the definition.
	Environment string
	// ForceAPI names an API that every request goes to, whatever the
	// definition says. Empty leaves the choice to the definition.
	ForceAPI string
	// HTTP sends the requests. Nil means a client of the Engine's own that
	// takes its timeouts and the number of its connections from the
	// definition, and that does not pass the credential on when a redirect
	// leads to another host.
	HTTP Doer
	// Insecure makes the Engine's own client accept any TLS certificate.
	// It has no effect on HTTP.
	Insecure bool
	// Tokens supplies the credential. Nil sends requests without one.
	Tokens TokenSource
	// Authorize adds the credential to a request. Nil means the header
	// "Authorization: Bearer" followed by the token.
	Authorize func(req *http.Request, token string)
	// Log receives one line per request at debug level, with the method,
	// the URL and the status, and never a credential. Nil discards them.
	Log *slog.Logger
	// Progress is told how far a long fetch has come. Fetches that run at
	// the same time call it at the same time. Nil means nobody is told.
	Progress func(Progress)
	// Sleep waits before a request is sent again. It returns early, with
	// the error of the context, when the context ends. Nil means a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// Stdin is read when a call takes a value from "-". Nil means there is
	// nothing to read.
	Stdin io.Reader
	// Templates holds the call templates a tool ships, as YAML files at
	// its root. Nil means it ships none.
	Templates fs.FS
}

// Stage says what an [Engine] is busy with when it reports [Progress].
type Stage uint8

// The stages of a run.
const (
	// StageFetch is the fetching of a listing. Done counts the items
	// fetched so far, and Total is 0: how many there are shows only when
	// the last page arrives.
	StageFetch Stage = iota + 1
	// StageJoin is the fetching of the related items that joins add to a
	// result. Done counts the items of the result that are complete, and
	// Total is their number.
	StageJoin
)

// Progress is a report of how far an [Engine] has come with what it does.
type Progress struct {
	// Stage is what the Engine is busy with.
	Stage Stage
	// Done is how much of it is done.
	Done int
	// Total is how much there is to do, and 0 when that is not known.
	Total int
}

// Engine sends the requests of one run of a tool to the API a definition
// describes. It keeps the answers to its GET requests for as long as it
// lives, so a tool makes a new Engine for every run.
//
// An Engine is safe for concurrent use.
type Engine struct {
	doc         *definition.Document
	environment string
	forceAPI    string
	http        Doer
	tokens      TokenSource
	authorize   func(req *http.Request, token string)
	log         *slog.Logger
	progress    func(Progress)
	sleep       func(ctx context.Context, d time.Duration) error
	stdin       io.Reader
	templates   fs.FS

	// mu guards the answers that are kept and the requests under way.
	mu       sync.Mutex
	answers  map[string]answer
	inflight map[string]*flight
}

// New returns an Engine for the API that o.Definition describes. It
// returns an error for a missing definition and for an environment or an
// API the definition does not have.
func New(o Options) (*Engine, error) {
	doc := o.Definition
	if doc == nil {
		return nil, apikit.Errorf(apikit.KindDefinition, "the engine needs a definition")
	}
	if o.Environment != "" {
		var names []string
		for _, environment := range doc.Environments {
			names = append(names, environment.Name)
		}
		if !slices.Contains(names, o.Environment) {
			return nil, apikit.Errorf(apikit.KindUsage, "unknown environment %q, the definition has: %s",
				o.Environment, strings.Join(names, ", "))
		}
	}
	if names := doc.APINames(); o.ForceAPI != "" && !slices.Contains(names, o.ForceAPI) {
		return nil, apikit.Errorf(apikit.KindUsage, "unknown API %q, the definition has: %s",
			o.ForceAPI, strings.Join(names, ", "))
	}

	e := &Engine{
		doc:         doc,
		environment: o.Environment,
		forceAPI:    o.ForceAPI,
		http:        o.HTTP,
		tokens:      o.Tokens,
		authorize:   o.Authorize,
		log:         o.Log,
		progress:    o.Progress,
		sleep:       o.Sleep,
		stdin:       o.Stdin,
		templates:   o.Templates,
		answers:     map[string]answer{},
		inflight:    map[string]*flight{},
	}
	if e.http == nil {
		e.http = NewClient(doc.HTTP, o.Insecure)
	}
	if e.authorize == nil {
		e.authorize = func(req *http.Request, token string) {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if e.log == nil {
		e.log = slog.New(slog.DiscardHandler)
	}
	if e.progress == nil {
		e.progress = func(Progress) {}
	}
	if e.sleep == nil {
		e.sleep = sleep
	}
	return e, nil
}

// fetched reports that items of a listing have arrived so far.
func (e *Engine) fetched(items int) {
	e.progress(Progress{Stage: StageFetch, Done: items})
}

// sleep waits for d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// seconds converts a number of seconds of a definition to a duration.
func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}
