package live

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackielii/structpages"
)

// pushList pushes its rows: Props is their only loader, and it renders each
// row for the viewer its cookie names.
type pushList struct{}

func (pushList) Live() []Fragment { return []Fragment{{Topic: "rows", Component: pushList.Row}} }

func (p pushList) Props(r *http.Request, t structpages.RenderTarget, s *rowStore) (row, error) {
	if !t.Is(p.Row) {
		return row{}, nil
	}
	name, err := Load(r.Context(), t, p.Row, s.Rows)
	return row{structpages.KeyOf(t), name + " for " + viewerOf(r)}, err
}

func (p pushList) Page() comp { return listPage{}.Page() }

func (p pushList) Row(rw row) comp {
	return func(ctx context.Context, w io.Writer) error {
		id, err := structpages.Key(ctx, pushList.Row, rw.key)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, `<tr id="%s"><td>%s</td></tr>`, id, rw.name)
		return err
	}
}

// itemPage is one item at /items/{itemId}: its fragment's URL needs the key
// as a path parameter, and Props reads it from the path.
type itemPage struct{}

func (itemPage) Live() []Fragment {
	return []Fragment{{Topic: "item", Component: itemPage.Detail, Params: func(key string) map[string]any {
		return map[string]any{"itemId": key}
	}}}
}

func (p itemPage) Props(r *http.Request, t structpages.RenderTarget, s *rowStore) (row, error) {
	id := r.PathValue("itemId")
	if t.Is(p.Detail) && structpages.KeyOf(t) != id {
		return row{}, fmt.Errorf("target %s on the page of %s", structpages.KeyOf(t), id)
	}
	rows, err := s.Rows([]string{id})
	if _, ok := rows[id]; !ok && err == nil {
		err = ErrNotFound
	}
	return row{id, rows[id] + " at " + r.URL.Path}, err
}

func (p itemPage) Page() comp { return listPage{}.Page() }

func (p itemPage) Detail(rw row) comp {
	return func(ctx context.Context, w io.Writer) error {
		id, err := structpages.Key(ctx, itemPage.Detail, rw.key)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, `<div id="%s">%s</div>`, id, rw.name)
		return err
	}
}

// postOnly cannot be fetched with GET, so its declaration is refused.
type postOnly struct{}

func (postOnly) Live() []Fragment                                       { return []Fragment{{Topic: "post", Component: postOnly.Row}} }
func (postOnly) ServeHTTP(w http.ResponseWriter, r *http.Request) error { return nil }
func (postOnly) Row(rw row) comp                                        { return listPage{}.Row(rw) }

type pushRoot struct {
	list   pushList    `route:"/list List"`
	item   itemPage    `route:"/items/{itemId} Item"`
	post   postOnly    `route:"POST /post Post"`
	plain  listPage    `route:"/plain Plain"`
	events eventsRoute `route:"GET /events Events"`
}

func viewerOf(r *http.Request) string {
	c, err := r.Cookie("viewer")
	if err != nil {
		return ""
	}
	return c.Value
}

// pushApp mounts pushRoot with h.Pages first and an access check after it,
// standing in for a host's per-request authorisation.
type pushApp struct {
	sp      *structpages.StructPages
	mux     *http.ServeMux
	store   *rowStore
	revoked map[string]bool
	checks  atomic.Int32
}

func newPushApp(t *testing.T, h *Hub[string]) *pushApp {
	t.Helper()
	a := &pushApp{mux: http.NewServeMux(), store: newStore(), revoked: map[string]bool{}}
	access := func(next http.Handler, _ *structpages.PageNode) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.checks.Add(1)
			if a.revoked[viewerOf(r)] {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	sp, err := structpages.Mount(a.mux, pushRoot{}, "/", "Test",
		structpages.WithArgs(a.store, h),
		structpages.WithTargetSelector(structpages.HTMXv4RenderTarget),
		structpages.WithMiddlewares(h.Pages, access, Middleware),
		structpages.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, ErrNotFound) {
				http.Error(w, "gone", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	a.sp = sp
	return a
}

// stream is the subscriber's stream request: the page tree in its context,
// its viewer in a cookie.
func (a *pushApp) stream(viewer string) *http.Request {
	r := httptest.NewRequest("GET", "/events", nil)
	r.Header.Set("Accept", "text/event-stream")
	r.AddCookie(&http.Cookie{Name: "viewer", Value: viewer})
	return r.WithContext(a.sp.PageContext(context.Background()))
}

func TestPagesFindsDeclarationsAtMount(t *testing.T) {
	h := NewHub[string](Config{})
	newPushApp(t, h)
	got := map[string]int{}
	for topic, regs := range h.pages {
		got[topic] = len(regs)
	}
	if want := map[string]int{"rows": 1, "item": 1}; !cmp.Equal(got, want) {
		t.Errorf("registered %v, want %v (a POST page and a page without Live are not)", got, want)
	}
}

func TestRenderThroughThePage(t *testing.T) {
	h := NewHub[string](Config{})
	a := newPushApp(t, h)
	got := h.Render(a.stream("ann"), "ann", Frame{Topic: "rows", IDs: []string{"a", "gone", "b/1"}})
	want := []Rendered{
		{Target: "list-row--a", HTML: `<tr id="list-row--a"><td>Alpha for ann</td></tr>`},
		{Target: "list-row--gone", Delete: true},
		{Target: "list-row--b_2f1", HTML: `<tr id="list-row--b_2f1"><td>Beta for ann</td></tr>`},
	}
	if !cmp.Equal(got, want) {
		t.Errorf("fragments:\n%s", cmp.Diff(want, got))
	}
	if n := a.checks.Load(); n != 3 {
		t.Errorf("the access check ran %d times, want once per fragment", n)
	}
	if want := [][]string{{"a", "gone", "b/1"}}; !cmp.Equal(a.store.queries, want) {
		t.Errorf("queries %v, want the frame's keys in one Load", a.store.queries)
	}
	if got := h.Render(a.stream("bob"), "bob", Frame{Topic: "rows", IDs: []string{"a"}}); len(got) != 1 || got[0].HTML != `<tr id="list-row--a"><td>Alpha for bob</td></tr>` {
		t.Errorf("bob's render %v", got)
	}
}

func TestRenderRechecksAccessEveryFrame(t *testing.T) {
	h := NewHub[string](Config{})
	a := newPushApp(t, h)
	f := Frame{Topic: "rows", IDs: []string{"a"}}
	if got := h.Render(a.stream("ann"), "ann", f); len(got) != 1 {
		t.Fatalf("before: %v", got)
	}
	a.revoked["ann"] = true
	if got := h.Render(a.stream("ann"), "ann", f); len(got) != 0 {
		t.Errorf("a revoked viewer's next frame must carry no fragment, got %v", got)
	}
}

func TestRenderFillsPathParamsFromTheKey(t *testing.T) {
	h := NewHub[string](Config{})
	a := newPushApp(t, h)
	got := h.Render(a.stream("ann"), "ann", Frame{Topic: "item", IDs: []string{"c", "nope"}})
	want := []Rendered{
		{Target: "item-detail--c", HTML: `<div id="item-detail--c">Gamma at /items/c</div>`},
		{Target: "item-detail--nope", Delete: true},
	}
	if !cmp.Equal(got, want) {
		t.Errorf("fragments:\n%s", cmp.Diff(want, got))
	}
}

func TestPageRequestIsAPartialAsTheSubscriber(t *testing.T) {
	h := NewHub[string](Config{})
	a := newPushApp(t, h)
	r := a.stream("ann")
	r.Header.Set("Authorization", "Bearer x")
	r.Header.Set("Last-Event-ID", "9")
	req, err := pageRequest(r, h.pages["item"][0], "a/b", "item-detail--a_2fb")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		"url":    req.URL.String(),
		"path":   req.PathValue("itemId"),
		"auth":   req.Header.Get("Authorization"),
		"cookie": viewerOf(req),
		"target": req.Header.Get("HX-Target"),
		"accept": req.Header.Get("Accept") + req.Header.Get("Last-Event-ID"),
	}
	want := map[string]string{"url": "/items/a%2Fb", "path": "a/b", "auth": "Bearer x", "cookie": "ann", "target": "#item-detail--a_2fb", "accept": ""}
	if !cmp.Equal(got, want) {
		t.Errorf("request:\n%s", cmp.Diff(want, got))
	}
}

// TestServeSSEPushesThroughPages: a stream served from the same Mount sends
// each viewer its own rendering, and a revoked viewer the bare signal.
func TestServeSSEPushesThroughPages(t *testing.T) {
	h := NewHub[string](Config{})
	a := newPushApp(t, h)
	srv := httptest.NewServer(a.mux)
	defer srv.Close()
	defer srv.CloseClientConnections()
	open := func(viewer string) func() sseEvent {
		req, _ := http.NewRequest("GET", srv.URL+"/events?since="+h.Since(), nil)
		req.Header.Set("Viewer", viewer)
		req.AddCookie(&http.Cookie{Name: "viewer", Value: viewer})
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		sc := newScanner(res.Body)
		return func() sseEvent { return readEvent(t, sc) }
	}
	ann, bob := open("ann"), open("bob")
	waitSubscribers(t, h, 2)
	a.revoked["bob"] = true
	h.Publish(context.Background(), everyone, "rows", "a")
	if e := ann(); e.data != `{"ids":["a"],"fragments":[{"target":"list-row--a","html":"<tr id=\"list-row--a\"><td>Alpha for ann</td></tr>"}]}` {
		t.Errorf("ann: %+v", e)
	}
	if e := bob(); e.data != `{"ids":["a"]}` {
		t.Errorf("bob, revoked: %+v", e)
	}
}

func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	return sc
}

func waitSubscribers(t *testing.T, h *Hub[string], n int) {
	t.Helper()
	for range 200 {
		h.mu.Lock()
		got := len(h.subs)
		h.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fewer than %d subscribers", n)
}
