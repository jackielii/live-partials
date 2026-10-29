package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackielii/structpages"
)

type comp func(ctx context.Context, w io.Writer) error

func (c comp) Render(ctx context.Context, w io.Writer) error { return c(ctx, w) }

var (
	errBoom      = errors.New("boom")
	errSignedOut = errors.New("signed out")
	errMoved     = errors.New("moved")
)

// rowStore counts its loads, standing in for a database.
type rowStore struct {
	rows    map[string]string
	queries [][]string
}

func (s *rowStore) Rows(keys []string) (map[string]string, error) {
	s.queries = append(s.queries, slices.Clone(keys))
	out := map[string]string{}
	for _, k := range keys {
		switch k {
		case "boom":
			return nil, errBoom
		}
		if v, ok := s.rows[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

type listPage struct{}

type row struct{ key, name string }

func (p listPage) Props(r *http.Request, w http.ResponseWriter, t structpages.RenderTarget, s *rowStore) (row, error) {
	switch {
	case t.Is(p.Row):
		key := structpages.KeyOf(t)
		switch key {
		case "signedout":
			return row{}, errSignedOut
		case "moved":
			return row{}, errMoved
		case "fail":
			return row{}, errBoom
		case "tick":
			w.Header().Set("HX-Trigger", `{"tick":{"n":1}}`)
		case "ping":
			w.Header().Set("HX-Trigger", "ping, pong")
		}
		name, err := Load(r.Context(), t, p.Row, s.Rows)
		return row{key, name}, err
	case t.Is(p.Total):
		return row{name: "total"}, nil
	}
	return row{}, nil
}

func (p listPage) Page() comp {
	return func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<!DOCTYPE html><html><body>whole page</body></html>")
		return err
	}
}

func (p listPage) Row(rw row) comp {
	return func(ctx context.Context, w io.Writer) error {
		id, err := structpages.Key(ctx, listPage.Row, rw.key)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, `<tr id="%s"><td>%s</td></tr>`, id, rw.name)
		return err
	}
}

func (p listPage) Total(rw row) comp {
	return func(ctx context.Context, w io.Writer) error {
		id, err := structpages.ID(ctx, listPage.Total)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, `<p id="%s">%s</p>`, id, rw.name)
		return err
	}
}

type root struct {
	list listPage `route:"/list List"`
}

// counters count how often the host's outer chain and the page handler run.
type counters struct{ outer, inner atomic.Int32 }

// app is a structpages app with the batch middleware, behind a cookie check
// standing in for a host's authentication.
func app(t *testing.T, s *rowStore, c *counters, opts ...Batch) http.Handler {
	t.Helper()
	b := Batch{}
	if len(opts) > 0 {
		b = opts[0]
	}
	mux := http.NewServeMux()
	inner := func(next http.Handler, _ *structpages.PageNode) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.inner.Add(1)
			next.ServeHTTP(w, r)
		})
	}
	_, err := structpages.Mount(mux, root{}, "/", "Test",
		structpages.WithArgs(s),
		structpages.WithTargetSelector(structpages.HTMXv4RenderTarget),
		structpages.WithMiddlewares(b.Middleware, inner),
		structpages.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			switch {
			case errors.Is(err, ErrNotFound):
				http.Error(w, "gone", http.StatusNotFound)
			case errors.Is(err, errSignedOut):
				http.Error(w, "sign in", http.StatusUnauthorized)
			case errors.Is(err, errMoved):
				w.Header().Set("HX-Location", "/elsewhere")
			default:
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.outer.Add(1)
		if ck, err := r.Cookie("session"); err != nil || ck.Value != "ok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func newStore() *rowStore {
	return &rowStore{rows: map[string]string{"a": "Alpha", "b/1": "Beta", "c": "Gamma", "tick": "Tick", "ping": "Ping"}}
}

func get(h http.Handler, targets string, cookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/list", nil)
	req.Header.Set("HX-Request", "true")
	if targets != "" {
		req.Header.Set(TargetsHeader, targets)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func partial(id, body string) string {
	return fmt.Sprintf(`<hx-partial hx-target="#%s" hx-swap="outerMorph">%s</hx-partial>`, id, body)
}

func deleted(id string) string {
	return fmt.Sprintf(`<hx-partial hx-target="#%s" hx-swap="delete"></hx-partial>`, id)
}

func TestMiddleware(t *testing.T) {
	tests := []struct {
		name    string
		targets string
		cookie  bool
		status  int
		want    string
		trigger string
	}{
		{
			name:    "keyed rows and an unkeyed region render in request order",
			targets: "list-row--a list-total list-row--b_2f1",
			cookie:  true,
			status:  200,
			want: partial("list-row--a", `<tr id="list-row--a"><td>Alpha</td></tr>`) +
				partial("list-total", `<p id="list-total">total</p>`) +
				partial("list-row--b_2f1", `<tr id="list-row--b_2f1"><td>Beta</td></tr>`),
		},
		{
			name:    "a 404 removes its element",
			targets: "list-row--zz list-row--a",
			cookie:  true,
			status:  200,
			want:    deleted("list-row--zz") + partial("list-row--a", `<tr id="list-row--a"><td>Alpha</td></tr>`),
		},
		{
			name:    "a failing target is skipped; the others still render",
			targets: "list-row--fail list-row--a",
			cookie:  true,
			status:  200,
			want:    partial("list-row--a", `<tr id="list-row--a"><td>Alpha</td></tr>`),
		},
		{
			name:    "a response whose root is not the target (the whole page) is skipped",
			targets: "nothing-here list-row--c",
			cookie:  true,
			status:  200,
			want:    partial("list-row--c", `<tr id="list-row--c"><td>Gamma</td></tr>`),
		},
		{
			name:    "HX-Trigger from every target is merged",
			targets: "list-row--ping list-row--tick",
			cookie:  true,
			status:  200,
			want: partial("list-row--ping", `<tr id="list-row--ping"><td>Ping</td></tr>`) +
				partial("list-row--tick", `<tr id="list-row--tick"><td>Tick</td></tr>`),
			trigger: `{"ping":{},"pong":{},"tick":{"n":1}}`,
		},
		{
			name:    "a 401 from a target aborts the batch and keeps earlier triggers",
			targets: "list-row--ping list-row--signedout list-row--a",
			cookie:  true,
			status:  401,
			want:    "sign in\n",
			trigger: "ping, pong",
		},
		{
			name:    "HX-Location from a target aborts the batch",
			targets: "list-row--a list-row--moved list-row--c",
			cookie:  true,
			status:  200,
			want:    "",
		},
		{
			name:    "the outer chain decides: no session, no content",
			targets: "list-row--a",
			status:  401,
			want:    "no\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(app(t, newStore(), &counters{}), tt.targets, tt.cookie)
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			got := rec.Body.String()
			if got != tt.want {
				t.Errorf("body\n got %s\nwant %s", got, tt.want)
			}
			if h := rec.Header().Get("HX-Trigger"); h != tt.trigger {
				t.Errorf("HX-Trigger = %q, want %q", h, tt.trigger)
			}
		})
	}
}

func TestMiddlewareAbortSendsTheTargetsResponse(t *testing.T) {
	rec := get(app(t, newStore(), &counters{}), "list-row--a list-row--moved list-row--c", true)
	if got := rec.Header().Get("HX-Location"); got != "/elsewhere" {
		t.Errorf("HX-Location = %q", got)
	}
	if strings.Contains(rec.Body.String(), "hx-partial") {
		t.Errorf("partials sent with an abort: %s", rec.Body)
	}
}

// TestBatchedLoadFailsItsTargetsOnly: a loader error fails every target
// that shares the query, and nothing else.
func TestBatchedLoadFailsItsTargetsOnly(t *testing.T) {
	rec := get(app(t, newStore(), &counters{}), "list-row--a list-total list-row--boom", true)
	if want := partial("list-total", `<p id="list-total">total</p>`); rec.Body.String() != want {
		t.Errorf("body\n got %s\nwant %s", rec.Body, want)
	}
}

func TestMiddlewareRunsTheOuterChainOnce(t *testing.T) {
	c := &counters{}
	get(app(t, newStore(), c), "list-row--a list-row--c list-total", true)
	if got := c.outer.Load(); got != 1 {
		t.Errorf("outer chain ran %d times, want 1", got)
	}
	if got := c.inner.Load(); got != 3 {
		t.Errorf("middleware inside the batch ran %d times, want once per target (3)", got)
	}
}

func TestLoadBatchesQueries(t *testing.T) {
	s := newStore()
	h := app(t, s, &counters{})
	get(h, "list-row--a list-total list-row--c list-row--zz list-row--a", true)
	if want := [][]string{{"a", "c", "zz"}}; !cmp.Equal(s.queries, want) {
		t.Errorf("queries %v, want one for every keyed row: %v", s.queries, want)
	}

	// An ordinary htmx request for one row loads only that row.
	s.queries = nil
	req := httptest.NewRequest("GET", "/list", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "tr#list-row--c")
	req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != `<tr id="list-row--c"><td>Gamma</td></tr>` {
		t.Errorf("single request body %s", rec.Body)
	}
	if want := [][]string{{"c"}}; !cmp.Equal(s.queries, want) {
		t.Errorf("queries %v, want %v", s.queries, want)
	}
}

func TestMiddlewarePassesThroughWithoutTargets(t *testing.T) {
	rec := get(app(t, newStore(), &counters{}), "", true)
	if !strings.Contains(rec.Body.String(), "whole page") {
		t.Errorf("body %s", rec.Body)
	}
}

func TestMiddlewareBounds(t *testing.T) {
	ids := strings.Repeat("list-row--a ", 3)
	if rec := get(app(t, newStore(), &counters{}, Batch{MaxTargets: 2}), ids, true); rec.Code != 400 {
		t.Errorf("over MaxTargets: %d", rec.Code)
	}
	rec := get(app(t, newStore(), &counters{}, Batch{MaxBytes: 60}), "list-row--a list-row--c", true)
	if got := rec.Header().Get("Live-Skipped"); got != "list-row--c" {
		t.Errorf("Live-Skipped = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "Alpha") || strings.Contains(rec.Body.String(), "Gamma") {
		t.Errorf("body %s", rec.Body)
	}
}

func TestKeys(t *testing.T) {
	var got []string
	spy := func(next http.Handler, _ *structpages.PageNode) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got == nil {
				got = Keys(r.Context(), listPage.Row)
			}
			next.ServeHTTP(w, r)
		})
	}
	mux := http.NewServeMux()
	if _, err := structpages.Mount(mux, root{}, "/", "Test",
		structpages.WithArgs(newStore()),
		structpages.WithTargetSelector(structpages.HTMXv4RenderTarget),
		structpages.WithMiddlewares(Middleware, spy)); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/list", nil)
	req.Header.Set(TargetsHeader, "list-row--c list-total list-row--b_2f1 list-row--c")
	mux.ServeHTTP(httptest.NewRecorder(), req)
	if want := []string{"c", "b/1"}; !cmp.Equal(got, want) {
		t.Errorf("Keys = %v, want %v", got, want)
	}
}
