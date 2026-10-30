package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gsxhq/vite"

	"github.com/jackielii/live-partials/examples/internal/fanout"
)

func labHandler(t *testing.T) (http.Handler, *app) {
	t.Helper()
	v, err := vite.New(vite.Config{Dist: distFS, DistDir: "dist", StaticURL: "/static/"})
	if err != nil {
		t.Fatal(err)
	}
	h, a, err := newHandler(v)
	if err != nil {
		t.Fatal(err)
	}
	return h, a
}

// stream is one tab's open page stream in a test.
type stream struct {
	t      *testing.T
	events *fanout.Reader
}

// openStream opens path's stream as v; since "" asks for a resync.
func openStream(t *testing.T, srv *httptest.Server, path, since string, v viewer) *stream {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path+"?since="+url.QueryEscape(since), nil)
	req.AddCookie(&http.Cookie{Name: viewerCookie, Value: string(v)})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &stream{t, fanout.NewReader(resp.Body)}
}

// push returns the Datastar events of the stream's next push.
func (s *stream) push() []fanout.Event {
	s.t.Helper()
	var out []fanout.Event
	for {
		e, err := s.events.Next()
		if err != nil {
			s.t.Fatal(err)
		}
		if e.Name == "lab-meter" {
			return out
		}
		out = append(out, e)
	}
}

func newServer(t *testing.T) (*httptest.Server, *app) {
	h, a := labHandler(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Cleanup(srv.CloseClientConnections)
	return srv, a
}

func simulate(t *testing.T, srv *httptest.Server, action, id string) {
	t.Helper()
	resp, err := srv.Client().PostForm(srv.URL+"/sim/"+action, url.Values{"id": {id}})
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatal(action, err, resp.Status)
	}
}

func TestConnectResyncsOnlyWhenSomethingChanged(t *testing.T) {
	srv, a := newServer(t)
	current := openStream(t, srv, "/series/updates", a.hub.Since(), "viewer-a")
	stale := openStream(t, srv, "/series/updates", "old", "viewer-b")
	got := stale.push()
	// sidebar, summary, rows: everything the page shows that can change.
	if len(got) != 3 || !strings.Contains(got[2].Data, `id="series-rows"`) {
		t.Fatalf("resync: %+v", got)
	}
	simulate(t, srv, "series-touch", "s3")
	for _, s := range []*stream{current, stale} {
		got := s.push()
		if len(got) != 2 || !strings.Contains(got[0].Data, `<tr id="series-row--s3">`) || !strings.Contains(got[1].Data, `id="series-summary"`) {
			t.Errorf("change: %+v", got)
		}
	}
}

func TestDraftSignalsAreRenderedPerViewer(t *testing.T) {
	srv, a := newServer(t)
	mine := openStream(t, srv, "/drafts/updates", a.hub.Since(), "viewer-a")
	theirs := openStream(t, srv, "/drafts/updates", a.hub.Since(), "viewer-b")

	req, _ := http.NewRequest("POST", srv.URL+"/drafts/d3", strings.NewReader(`{"drafts":{"d3":{"value":"42"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: viewerCookie, Value: "viewer-a"})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"by":"you"`) || !strings.Contains(string(body), `"base":"42"`) {
		t.Errorf("save response: %s", body)
	}
	for v, s := range map[string]*stream{"you": mine, "viewer-a": theirs} {
		got := s.push()
		want := `signals {"drafts":{"d3":{"server":"42","by":"` + v + `"}}}`
		if len(got) != 1 || got[0].Name != "datastar-patch-signals" || got[0].Data != want {
			t.Errorf("%s: %+v, want %s", v, got, want)
		}
	}
}

func TestFilesInsertMorphsTheListAndDeleteRemovesTheRow(t *testing.T) {
	srv, a := newServer(t)
	s := openStream(t, srv, "/files/updates", a.hub.Since(), "viewer-a")
	simulate(t, srv, "file-add", "")
	got := s.push()
	if len(got) != 2 || !strings.Contains(got[0].Data, `<ul id="files-list"`) || !strings.Contains(got[1].Data, `id="files-count"`) {
		t.Errorf("insert: %+v", got)
	}
	simulate(t, srv, "file-delete", "f2")
	got = s.push()
	if len(got) != 2 || got[0].Data != "selector #files-item--f2\nmode remove" || !strings.Contains(got[1].Data, `data-testid="file-count">3<`) {
		t.Errorf("delete: %+v", got)
	}
}

func TestCatalogStreamFollowsTheFilterSignal(t *testing.T) {
	srv, _ := newServer(t)
	path := "/catalog/updates?datastar=" + url.QueryEscape(`{"query":{"status":"stale"}}`)
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	req.AddCookie(&http.Cookie{Name: viewerCookie, Value: "viewer-a"})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	s := &stream{t, fanout.NewReader(resp.Body)}
	if got := s.push(); !strings.Contains(got[1].Data, `data-testid="catalog-count">0<`) {
		t.Fatalf("connect: %+v", got)
	}
	simulate(t, srv, "series-touch", "s1")
	got := s.push()
	if len(got) != 1 || !strings.Contains(got[0].Data, `<tr id="catalog-row--s1">`) || !strings.Contains(got[0].Data, "with status stale") {
		t.Errorf("change: %+v", got)
	}
}
