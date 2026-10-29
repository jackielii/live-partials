package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackielii/structpages"
)

type comp func(ctx context.Context, w io.Writer) error

func (c comp) Render(ctx context.Context, w io.Writer) error { return c(ctx, w) }

var rows = map[string]string{"a": "Alpha", "b/1": "Beta"}

var errMissing = errors.New("missing")

type listPage struct{}

type rowProps struct{ key, name string }

func (p listPage) Props(r *http.Request, t structpages.RenderTarget) (rowProps, error) {
	if t.Is(p.Row) {
		name, ok := rows[Key(r)]
		if !ok {
			return rowProps{}, errMissing
		}
		return rowProps{Key(r), name}, nil
	}
	return rowProps{}, nil
}

func (p listPage) Page() comp {
	return func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<!DOCTYPE html><html><body>whole page</body></html>")
		return err
	}
}

func (p listPage) Row(props rowProps) comp {
	return func(ctx context.Context, w io.Writer) error {
		id, err := ID(ctx, listPage.Row, props.key)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, `<tr id="%s"><td>%s</td></tr>`, id, props.name)
		return err
	}
}

type root struct {
	list listPage `route:"/list List"`
}

// app is a structpages app behind a cookie check, standing in for a host's
// full handler chain.
func app(t *testing.T, hits *atomic.Int32) http.Handler {
	mux := http.NewServeMux()
	_, err := structpages.Mount(mux, root{}, "/", "Test",
		structpages.WithTargetSelector(Selector(structpages.HTMXv4RenderTarget)),
		structpages.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, errMissing) {
				http.Error(w, "gone", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if c, err := r.Cookie("session"); err != nil || c.Value != "ok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func post(t *testing.T, h http.Handler, items []item, cookie bool) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(items)
	req := httptest.NewRequest("POST", "/live", strings.NewReader(url.Values{"live": {string(b)}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie {
		req.AddCookie(&http.Cookie{Name: "session", Value: "ok"})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBatchHandler(t *testing.T) {
	h := BatchHandler(app(t, nil))
	tests := []struct {
		name   string
		items  []item
		cookie bool
		want   string
	}{
		{
			name:   "a keyed row renders inside a morphing partial",
			items:  []item{{"/list", "tr#list-row--a"}},
			cookie: true,
			want:   `<hx-partial hx-target="#list-row--a" hx-swap="outerMorph"><tr id="list-row--a"><td>Alpha</td></tr></hx-partial>`,
		},
		{
			name:   "an escaped key round-trips",
			items:  []item{{"/list", "tr#list-row--b_2f1"}},
			cookie: true,
			want:   `<hx-partial hx-target="#list-row--b_2f1" hx-swap="outerMorph"><tr id="list-row--b_2f1"><td>Beta</td></tr></hx-partial>`,
		},
		{
			name:   "404 removes the element",
			items:  []item{{"/list", "tr#list-row--zz"}},
			cookie: true,
			want:   `<hx-partial hx-target="#list-row--zz" hx-swap="delete"></hx-partial>`,
		},
		{
			name:   "items keep their order",
			items:  []item{{"/list", "tr#list-row--zz"}, {"/list", "tr#list-row--a"}},
			cookie: true,
			want:   `<hx-partial hx-target="#list-row--zz" hx-swap="delete"></hx-partial><hx-partial hx-target="#list-row--a" hx-swap="outerMorph"><tr id="list-row--a"><td>Alpha</td></tr></hx-partial>`,
		},
		{
			name:  "the caller's cookies decide: no session, no content",
			items: []item{{"/list", "tr#list-row--a"}},
		},
		{
			name:   "a response that is not the target (the whole page) is skipped",
			items:  []item{{"/list", "div#nothing-here"}},
			cookie: true,
		},
		{
			name:   "absolute and relative URLs are refused",
			items:  []item{{"http://evil.example/list", "tr#list-row--a"}, {"list", "tr#list-row--a"}},
			cookie: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, h, tt.items, tt.cookie)
			if rec.Code != 200 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if got := rec.Body.String(); got != tt.want {
				t.Errorf("body\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestBatchHandlerRejectsBadRequests(t *testing.T) {
	h := &Batch{Next: app(t, nil), MaxItems: 2}
	if rec := post(t, h, nil, true); rec.Code != 400 {
		t.Errorf("empty list: %d", rec.Code)
	}
	three := []item{{"/list", "tr#list-row--a"}, {"/list", "tr#list-row--a"}, {"/list", "tr#list-row--a"}}
	if rec := post(t, h, three, true); rec.Code != 400 {
		t.Errorf("over MaxItems: %d", rec.Code)
	}
}

func TestBatchHandlerBoundsSize(t *testing.T) {
	h := &Batch{Next: app(t, nil), MaxBytes: 150}
	rec := post(t, h, []item{{"/list", "tr#list-row--a"}, {"/list", "tr#list-row--b_2f1"}}, true)
	if got := rec.Header().Get("Live-Skipped"); got != "tr#list-row--b_2f1" {
		t.Errorf("Live-Skipped = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "Alpha") || strings.Contains(rec.Body.String(), "Beta") {
		t.Errorf("body %s", rec.Body)
	}
}

func TestBatchHandlerBoundsConcurrency(t *testing.T) {
	var running, peak atomic.Int32
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		fmt.Fprintf(w, `<p id="%s"></p>`, strings.TrimPrefix(r.Header.Get("HX-Target"), "p#"))
	})
	items := make([]item, 12)
	for i := range items {
		items[i] = item{"/x", fmt.Sprintf("p#i%d", i)}
	}
	rec := post(t, &Batch{Next: slow, Concurrency: 3}, items, false)
	if got := strings.Count(rec.Body.String(), "outerMorph"); got != 12 {
		t.Errorf("rendered %d of 12", got)
	}
	if p := peak.Load(); p > 3 {
		t.Errorf("peak concurrency %d > 3", p)
	}
}

func TestBatchItemRequest(t *testing.T) {
	var got *http.Request
	spy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		fmt.Fprint(w, `<li id="x"></li>`)
	})
	post(t, BatchHandler(spy), []item{{"/files?folder=7", "li#x"}}, true)
	if got.Method != "GET" || got.URL.Path != "/files" || got.URL.Query().Get("folder") != "7" {
		t.Errorf("request %s %s", got.Method, got.URL)
	}
	for h, want := range map[string]string{"HX-Request": "true", "HX-Request-Type": "partial", "HX-Target": "li#x", "Content-Type": ""} {
		if v := got.Header.Get(h); v != want {
			t.Errorf("%s = %q, want %q", h, v, want)
		}
	}
	if c, err := got.Cookie("session"); err != nil || c.Value != "ok" {
		t.Error("cookie not forwarded")
	}
}

// TestStockSelectorMissesKeyedTargets is the repro for
// docs/library-proposals.md: without Selector, structpages cannot match a
// keyed id to its component and serves the whole page into the row.
func TestStockSelectorMissesKeyedTargets(t *testing.T) {
	mux := http.NewServeMux()
	if _, err := structpages.Mount(mux, root{}, "/", "Test",
		structpages.WithTargetSelector(structpages.HTMXv4RenderTarget)); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/list", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "tr#list-row--a")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "<html>") {
		t.Fatalf("expected the stock selector to fall back to Page, got %s", rec.Body)
	}
}
