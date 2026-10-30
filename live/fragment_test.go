package live

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackielii/structpages"
)

func TestValueFramesMergeLaterValueWins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHub[string](Config{})
		s := h.Subscribe("u")
		defer s.Close()
		ctx := context.Background()
		h.PublishValues(ctx, everyone, "presence", map[string]string{"p1": "idle"})
		if got := take(s); !cmp.Equal(got, []Frame{{Topic: "presence", Values: map[string]string{"p1": "idle"}}}) {
			t.Fatalf("first value frame goes at once, got %v", got)
		}
		h.PublishValues(ctx, everyone, "presence", map[string]string{"p1": "away", "p2": "active"})
		h.Publish(ctx, everyone, "presence", "p9") // a signal of the same topic coalesces apart
		h.PublishValues(ctx, everyone, "presence", map[string]string{"p1": "active"})
		if got := take(s); !cmp.Equal(got, []Frame{{Topic: "presence", IDs: []string{"p9"}}}) {
			t.Fatalf("the signal's first frame goes at once, values are held, got %v", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		want := []Frame{{Topic: "presence", Values: map[string]string{"p1": "active", "p2": "active"}}}
		if got := take(s); !cmp.Equal(got, want) {
			t.Errorf("held values:\n%s", cmp.Diff(want, got))
		}
	})
}

func TestPublishValuesAdmitsByKeys(t *testing.T) {
	h := NewHub[string](Config{})
	staff, guest := h.Subscribe("staff-1"), h.Subscribe("guest")
	defer staff.Close()
	defer guest.Close()
	var ids []string
	aud := audienceFunc(func(_ string, keys []string, v string) bool {
		ids = keys
		return strings.HasPrefix(v, "staff")
	})
	h.PublishValues(context.Background(), aud, "presence", map[string]string{"b": "1", "a": "2"})
	if !cmp.Equal(ids, []string{"a", "b"}) {
		t.Errorf("Admit got ids %v, want the sorted keys", ids)
	}
	if len(take(staff)) != 1 || len(take(guest)) != 0 {
		t.Error("only staff should hear the values")
	}
}

type audienceFunc func(topic string, ids []string, v string) bool

func (f audienceFunc) Admit(_ context.Context, topic string, ids []string, viewers []string) []bool {
	out := make([]bool, len(viewers))
	for i, v := range viewers {
		out[i] = f(topic, ids, v)
	}
	return out
}

func TestEndClosesMatchingStreamsAndForcesResync(t *testing.T) {
	h := NewHub[string](Config{})
	a, b := h.Subscribe("a"), h.Subscribe("b")
	defer a.Close()
	defer b.Close()
	since := h.Since()
	if n := h.End(func(v string) bool { return v == "a" }); n != 1 {
		t.Errorf("ended %d", n)
	}
	select {
	case <-a.Ended():
	default:
		t.Error("a should have ended")
	}
	select {
	case <-b.Ended():
		t.Error("b should not have ended")
	default:
	}
	if h.Since() == since {
		t.Error("a reconnect after End must resync")
	}
}

// pageCtx is a context carrying the test app's page tree, as a structpages
// route's request context does.
func pageCtx(t *testing.T) context.Context {
	t.Helper()
	sp, err := structpages.Parse(root{}, "/", "Test")
	if err != nil {
		t.Fatal(err)
	}
	return sp.PageContext(context.Background())
}

// rowFragment renders listPage.Row for key as viewer sees it.
func rowFragment(rows map[string]string) FragmentFunc[string] {
	return func(_ context.Context, viewer, key string) (Component, error) {
		name, ok := rows[key]
		if !ok {
			return nil, ErrNotFound
		}
		return listPage{}.Row(row{key, name + " for " + viewer}), nil
	}
}

func TestRender(t *testing.T) {
	ctx := pageCtx(t)
	h := NewHub[string](Config{})
	h.Fragment("rows", listPage.Row, rowFragment(map[string]string{"a": "Alpha", "b/1": "Beta"}))
	h.Fragment("bad", listPage.Row, func(context.Context, string, string) (Component, error) {
		return comp(func(_ context.Context, w io.Writer) error {
			_, err := io.WriteString(w, `<tr id="other">x</tr>`)
			return err
		}), nil
	})

	got := h.Render(streamReq(ctx), "ann", Frame{Topic: "rows", IDs: []string{"a", "gone", "b/1"}})
	want := []Rendered{
		{Target: "list-row--a", HTML: `<tr id="list-row--a"><td>Alpha for ann</td></tr>`},
		{Target: "list-row--b_2f1", HTML: `<tr id="list-row--b_2f1"><td>Beta for ann</td></tr>`},
	}
	if !cmp.Equal(got, want) {
		t.Errorf("fragments (a missing key is left to pull):\n%s", cmp.Diff(want, got))
	}
	for name, f := range map[string]Frame{
		"an id-less frame stays a signal": {Topic: "rows"},
		"an unregistered topic":           {Topic: "other", IDs: []string{"a"}},
		"a value frame":                   {Topic: "rows", Values: map[string]string{"a": "1"}},
		"a root that is not the target":   {Topic: "bad", IDs: []string{"a"}},
		"no page tree in the context":     {Topic: "rows", IDs: []string{"a"}},
	} {
		c := ctx
		if name == "no page tree in the context" {
			c = context.Background()
		}
		if got := h.Render(streamReq(c), "ann", f); len(got) != 0 {
			t.Errorf("%s: got %v", name, got)
		}
	}

	small := NewHub[string](Config{MaxFragment: 60})
	small.Fragment("rows", listPage.Row, rowFragment(map[string]string{"a": "Alpha", "b/1": "Beta"}))
	if got := small.Render(streamReq(ctx), "ann", Frame{Topic: "rows", IDs: []string{"a", "b/1"}}); len(got) != 1 {
		t.Errorf("past MaxFragment the rest are pulled, got %d fragments", len(got))
	}
}

// streamReq is a stream request with ctx.
func streamReq(ctx context.Context) *http.Request {
	return httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
}

type sseEvent struct{ name, data string }

func readEvent(t *testing.T, sc *bufio.Scanner) sseEvent {
	t.Helper()
	var e sseEvent
	for sc.Scan() {
		l := sc.Text()
		switch {
		case l == "" && e.name != "":
			return e
		case strings.HasPrefix(l, "event: "):
			e.name = l[7:]
		case strings.HasPrefix(l, "data: "):
			e.data = l[6:]
		}
	}
	t.Fatal("stream ended")
	return e
}

// TestServeSSEPushes runs two viewers' streams from a structpages route: each
// gets fragments rendered as itself, value frames reach both, and a resync
// carries the current values.
func TestServeSSEPushes(t *testing.T) {
	h := NewHub[string](Config{})
	h.Fragment("rows", listPage.Row, rowFragment(map[string]string{"a": "Alpha"}))
	h.Current("presence", func(_ context.Context, viewer string) (map[string]string, error) {
		return map[string]string{"p1": "idle", "seen-by": viewer}, nil
	})
	mux := http.NewServeMux()
	if _, err := structpages.Mount(mux, streamRoot{}, "/", "Test", structpages.WithArgs(h, newStore())); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer srv.CloseClientConnections()
	open := func(viewer, since string) *bufio.Scanner {
		req, _ := http.NewRequest("GET", srv.URL+"/events?since="+since, nil)
		req.Header.Set("Viewer", viewer)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return bufio.NewScanner(res.Body)
	}
	ann := open("ann", "")
	if e := readEvent(t, ann); e.name != "resync" || e.data != `{"values":{"presence":{"p1":"idle","seen-by":"ann"}}}` {
		t.Fatalf("resync %+v", e)
	}
	bob := open("bob", "")
	readEvent(t, bob)

	h.Publish(context.Background(), AudienceFunc[string](func(v string) bool { return v == "ann" }), "rows", "a")
	h.PublishValues(context.Background(), everyone, "presence", map[string]string{"p1": "active"})
	if e := readEvent(t, ann); e.name != "rows" || !strings.Contains(e.data, "Alpha for ann") || !strings.Contains(e.data, `"ids":["a"]`) {
		t.Errorf("ann's fragment frame %+v", e)
	}
	for name, sc := range map[string]*bufio.Scanner{"ann": ann, "bob": bob} {
		e := readEvent(t, sc)
		var d struct{ Values map[string]string }
		if err := json.Unmarshal([]byte(e.data), &d); err != nil || e.name != "presence" || d.Values["p1"] != "active" {
			t.Errorf("%s: value frame %+v", name, e)
		}
	}
	if n := h.End(func(v string) bool { return v == "bob" }); n != 1 {
		t.Fatalf("ended %d", n)
	}
	if bob.Scan() {
		t.Errorf("bob's stream should end, got %q", bob.Text())
	}
}

type streamRoot struct {
	list   listPage    `route:"/list List"`
	events eventsRoute `route:"GET /events Events"`
}

type eventsRoute struct{}

func (eventsRoute) ServeHTTP(w http.ResponseWriter, r *http.Request, h *Hub[string]) error {
	return h.ServeSSE(w, r, r.Header.Get("Viewer"))
}

func TestWriteFrame(t *testing.T) {
	var b strings.Builder
	WriteFrame(&b, Frame{Topic: "t", IDs: []string{"a"}}, Rendered{Target: "x--a", HTML: "<p id=\"x--a\">\n</p>"})
	WriteFrame(&b, Frame{Topic: "v", Values: map[string]string{"a": "1"}})
	WriteFrame(&b, Frame{Topic: "w"})
	want := fmt.Sprint(
		"event: t\ndata: {\"ids\":[\"a\"],\"fragments\":[{\"target\":\"x--a\",\"html\":\"<p id=\\\"x--a\\\">\\n</p>\"}]}\n\n",
		"event: v\ndata: {\"values\":{\"a\":\"1\"}}\n\n",
		"event: w\ndata: {}\n\n",
	)
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}
