package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gsxhq/vite"
)

func labHandler(t *testing.T) http.Handler {
	t.Helper()
	v, err := vite.New(vite.Config{Dist: distFS, DistDir: "dist", StaticURL: "/static/"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := newHandler(v)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func queries(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	n, err := strconv.Atoi(rec.Header().Get("X-Queries"))
	if err != nil {
		t.Fatalf("X-Queries %q: %v", rec.Header().Get("X-Queries"), err)
	}
	return n
}

// TestBatchedLoading measures what live.Load saves: three changed series rows
// and the summary cost two store queries in one batch, against four when
// each element is refetched on its own.
func TestBatchedLoading(t *testing.T) {
	h := labHandler(t)
	targets := []string{"series-row--s2", "series-row--s5", "series-row--s8", "series-summary"}

	req := httptest.NewRequest("GET", "/series", nil)
	req.Header.Set("X-Live-Targets", strings.Join(targets, " "))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := strings.Count(rec.Body.String(), `hx-swap="outerMorph"`); got != 4 {
		t.Fatalf("%d partials, want 4: %s", got, rec.Body)
	}
	if got := queries(t, rec); got != 2 {
		t.Errorf("batched: %d queries, want 2", got)
	}

	one := 0
	for _, id := range targets {
		req := httptest.NewRequest("GET", "/series", nil)
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Target", "#"+id)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), `id="`+id+`"`) || strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s: %s", id, rec.Body)
		}
		one += queries(t, rec)
	}
	if one != 4 {
		t.Errorf("one by one: %d queries, want 4", one)
	}
}

func TestDeletedRowIs404(t *testing.T) {
	h := labHandler(t)
	req := httptest.NewRequest("GET", "/series", nil)
	req.Header.Set("X-Live-Targets", "series-row--nope")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if want := `<hx-partial hx-target="#series-row--nope" hx-swap="delete"></hx-partial>`; rec.Body.String() != want {
		t.Errorf("body %s", rec.Body)
	}
}
