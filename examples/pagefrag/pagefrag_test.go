package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/gsxhq/vite"

	"github.com/jackielii/live-partials/examples/internal/fanout"
	"github.com/jackielii/live-partials/live"
)

func labHandler(t testing.TB, opt options) (http.Handler, *app) {
	t.Helper()
	v, err := vite.New(vite.Config{Dist: distFS, DistDir: "dist", StaticURL: "/static/"})
	if err != nil {
		t.Fatal(err)
	}
	h, a, err := newHandler(v, opt)
	if err != nil {
		t.Fatal(err)
	}
	return h, a
}

func newServer(t *testing.T, opt options) (*httptest.Server, *app) {
	h, a := labHandler(t, opt)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(srv.CloseClientConnections)
	return srv, a
}

// stream is one tab's open stream in a test.
type stream struct {
	t      *testing.T
	events *fanout.Reader
}

type frameData struct {
	IDs       []string
	Values    map[string]string
	Fragments []live.Rendered
}

// openStream opens the stream as v; since "" asks for a resync.
func openStream(t *testing.T, srv *httptest.Server, since string, v viewer) *stream {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+"/events?since="+url.QueryEscape(since), nil)
	req.AddCookie(&http.Cookie{Name: viewerCookie, Value: string(v)})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &stream{t, fanout.NewReader(resp.Body)}
}

// next returns the stream's next event other than the meter's.
func (s *stream) next() (string, frameData) {
	s.t.Helper()
	for {
		e, err := s.events.Next()
		if err != nil {
			s.t.Fatal(err)
		}
		if e.Name == "lab-meter" {
			continue
		}
		var d frameData
		if e.Name == "resync" {
			return e.Name, d
		}
		if err := json.Unmarshal([]byte(e.Data), &d); err != nil {
			s.t.Fatalf("%s: %v", e.Data, err)
		}
		return e.Name, d
	}
}

func post(t *testing.T, srv *httptest.Server, path string, form url.Values, v viewer) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: viewerCookie, Value: string(v)})
	resp, err := srv.Client().Do(req)
	if err != nil || resp.StatusCode >= 300 {
		t.Fatal(path, err, resp.Status)
	}
	resp.Body.Close()
}

func since(t *testing.T, srv *httptest.Server) string {
	return fanout.Since(t, srv.Client(), srv.URL+"/drafts")
}

// TestFragmentsAreRenderedPerViewer: a row saved by ann reaches ann's
// stream as "last saved by you" and bob's as "last saved by ann"; neither
// stream ever carries the other's rendering.
func TestFragmentsAreRenderedPerViewer(t *testing.T) {
	srv, _ := newServer(t, options{})
	at := since(t, srv)
	ann, bob := openStream(t, srv, at, "ann"), openStream(t, srv, at, "bob")
	post(t, srv, "/drafts/d3", url.Values{"value": {"42"}}, "ann")
	for name, tc := range map[string]struct {
		s          *stream
		want, deny string
	}{
		"ann": {ann, "last saved by you", "last saved by ann"},
		"bob": {bob, "last saved by ann", "last saved by you"},
	} {
		topic, d := tc.s.next()
		if topic != "lab:draft" || len(d.Fragments) != 1 || d.Fragments[0].Target != "drafts-row--d3" {
			t.Fatalf("%s: %s %+v", name, topic, d)
		}
		html := d.Fragments[0].HTML
		if !strings.Contains(html, tc.want) || strings.Contains(html, tc.deny) || !strings.Contains(html, `value="42"`) {
			t.Errorf("%s got %s", name, html)
		}
	}
}

// TestFragmentOfAMissingJobDeletes: the jobs page answers 404 for a job the
// store does not have, so the frame tells the tab to delete its card, as a
// pull's 404 would, and the frame still names both ids.
func TestFragmentOfAMissingJobDeletes(t *testing.T) {
	srv, a := newServer(t, options{})
	s := openStream(t, srv, since(t, srv), "ann")
	a.hub.Publish(context.Background(), everyone, "lab:job", "j1", "gone")
	topic, d := s.next()
	want := []live.Rendered{{Target: "jobs-card--j1"}, {Target: "jobs-card--gone", Delete: true}}
	if topic != "lab:job" || strings.Join(d.IDs, " ") != "j1 gone" || len(d.Fragments) != 2 {
		t.Fatalf("%s %+v", topic, d)
	}
	d.Fragments[0].HTML = ""
	if !cmp.Equal(d.Fragments, want) {
		t.Errorf("fragments %+v", d.Fragments)
	}
}

// TestRevokedViewerGetsNoFragment: the jobs page's middleware runs on every
// push, so once bob's access is revoked his very next frame is the bare
// signal, and his pull is refused too; ann still gets her fragment.
func TestRevokedViewerGetsNoFragment(t *testing.T) {
	for _, opt := range []options{{}, {direct: true}} {
		srv, _ := newServer(t, opt)
		at := since(t, srv)
		ann, bob := openStream(t, srv, at, "ann"), openStream(t, srv, at, "bob")
		post(t, srv, "/sim/revoke-jobs", url.Values{"id": {"bob"}}, "ann")
		post(t, srv, "/sim/job-step", url.Values{"id": {"j1"}}, "ann")
		if _, d := ann.next(); len(d.Fragments) != 1 || !strings.Contains(d.Fragments[0].HTML, "done") {
			t.Errorf("%+v ann: %+v", opt, d)
		}
		if topic, d := bob.next(); topic != "lab:job" || len(d.Fragments) != 0 || strings.Join(d.IDs, " ") != "j1" {
			t.Errorf("%+v bob: %s %+v", opt, topic, d)
		}
		req, _ := http.NewRequest("GET", srv.URL+"/jobs", nil)
		req.Header.Set(live.TargetsHeader, "jobs-card--j1")
		req.AddCookie(&http.Cookie{Name: viewerCookie, Value: "bob"})
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(b) != 0 {
			t.Errorf("%+v bob's pull got %s", opt, b)
		}
	}
}

// TestPageAndDirectRenderTheSame: the fragment rendered through the page is
// byte for byte the one the hybrid stage's render function made.
func TestPageAndDirectRenderTheSame(t *testing.T) {
	html := map[bool]string{}
	for _, direct := range []bool{false, true} {
		srv, _ := newServer(t, options{direct: direct})
		at := since(t, srv)
		bob := openStream(t, srv, at, "bob")
		post(t, srv, "/drafts/d3", url.Values{"value": {"42"}}, "ann")
		_, d := bob.next()
		if len(d.Fragments) != 1 {
			t.Fatalf("direct=%v: %+v", direct, d)
		}
		html[direct] = d.Fragments[0].HTML
	}
	if html[false] != html[true] {
		t.Errorf("page:\n%s\ndirect:\n%s", html[false], html[true])
	}
}

// TestValueFrames: a presence change is one value frame with the value; a
// resync carries every current value; the stream renders and queries nothing
// per subscriber for it.
func TestValueFrames(t *testing.T) {
	srv, a := newServer(t, options{})
	s := openStream(t, srv, "", "ann")
	if topic, _ := s.next(); topic != "resync" {
		t.Fatalf("want a resync first, got %s", topic)
	}
	q := a.store.TotalQueries()
	post(t, srv, "/sim/presence", url.Values{"id": {"p1"}}, "bob")
	topic, d := s.next()
	if topic != "lab:presence" || len(d.Values) != 1 || d.Values["p1"] != "idle" || d.IDs != nil || d.Fragments != nil {
		t.Errorf("%s %+v", topic, d)
	}
	if got := a.store.TotalQueries() - q; got != 2 { // the change and the value read, at the write site
		t.Errorf("%d queries, want 2", got)
	}
}

func TestResyncCarriesCurrentValues(t *testing.T) {
	srv, _ := newServer(t, options{})
	req, _ := http.NewRequest("GET", srv.URL+"/events", nil)
	req.AddCookie(&http.Cookie{Name: viewerCookie, Value: "ann"})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	e, err := fanout.NewReader(resp.Body).Next()
	if err != nil {
		t.Fatal(err)
	}
	var d struct{ Values map[string]map[string]string }
	if err := json.Unmarshal([]byte(e.Data), &d); err != nil {
		t.Fatal(err)
	}
	p := d.Values["lab:presence"]
	if e.Name != "resync" || len(p) != 56 || p["p1"] != "active" || p["c50"] == "" {
		t.Errorf("%s: %d values %v", e.Name, len(p), p["p1"])
	}
}

// BenchmarkRender is one subscriber's render of one fragment: through the
// page's handler (Props, the page's middleware, target selection) against
// the hybrid stage's direct render function.
func BenchmarkRender(b *testing.B) {
	for _, c := range []struct{ topic, id string }{{"lab:job", "j1"}, {"lab:draft", "d2"}} {
		for _, mode := range []struct {
			name string
			opt  options
		}{{"page", options{}}, {"direct", options{direct: true}}} {
			b.Run(c.topic+"/"+mode.name, func(b *testing.B) {
				_, a := labHandler(b, mode.opt)
				r := httptest.NewRequest("GET", "/events", nil)
				r.AddCookie(&http.Cookie{Name: viewerCookie, Value: "ann"})
				r = r.WithContext(a.pages.PageContext(context.Background()))
				f := live.Frame{Topic: c.topic, IDs: []string{c.id}}
				b.ReportAllocs()
				for b.Loop() {
					if got := a.hub.Render(r, "ann", f); len(got) != 1 {
						b.Fatal(got)
					}
				}
			})
		}
	}
}
