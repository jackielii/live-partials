package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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

// TestFragmentFallsBackToSignal: a job the store does not have renders no
// fragment; the frame still names it, so the page refetches and gets 404.
func TestFragmentFallsBackToSignal(t *testing.T) {
	srv, a := newServer(t, options{})
	s := openStream(t, srv, since(t, srv), "ann")
	a.hub.Publish(context.Background(), everyone, "lab:job", "j1", "gone")
	topic, d := s.next()
	if topic != "lab:job" || strings.Join(d.IDs, " ") != "j1 gone" || len(d.Fragments) != 1 || d.Fragments[0].Target != "jobs-card--j1" {
		t.Errorf("%s %+v", topic, d)
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
