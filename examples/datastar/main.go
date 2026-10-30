// Command datastar is the lab's six cases (and cases 7 and 8) written the
// Datastar way: each page opens one long-lived SSE stream and the server
// pushes the changed elements (datastar-patch-elements) or signals
// (datastar-patch-signals) down it. Nothing is refetched.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gsxhq/vite"
	"github.com/jackielii/structpages"

	"github.com/jackielii/live-partials/examples/labdata"
	"github.com/jackielii/live-partials/live"
)

// viewer is the signed-in user: here a name kept in a cookie, so two browser
// contexts are two users and per-viewer rendering is visible.
type viewer string

type (
	store         = labdata.Store
	series        = labdata.Series
	seriesSummary = labdata.SeriesSummary
	person        = labdata.Person
	draftRow      = labdata.DraftRow
	file          = labdata.File
	burstRow      = labdata.BurstRow
	job           = labdata.Job
	dropper       = labdata.Dropper
)

var errNotFound = labdata.ErrNotFound

// hub is the same pub/sub (with per-subscriber coalescing) as the htmx
// stages; a Datastar app needs one too, and this keeps the comparison about
// the browser side and the push.
type hub = live.Hub[viewer]

var everyone = live.AudienceFunc[viewer](func(viewer) bool { return true })

//go:embed all:dist
var distFS embed.FS

type pages struct {
	home    homePage    `route:"/{$} Datastar lab"`
	series  seriesPage  `route:"/series Series catalog"`
	people  peoplePage  `route:"/people People"`
	drafts  draftsPage  `route:"/drafts Report draft"`
	save    saveDraft   `route:"POST /drafts/{rowId} Save draft row"`
	files   filesPage   `route:"/files Files"`
	burst   burstPage   `route:"/burst Burst"`
	jobs    jobsPage    `route:"/jobs Jobs"`
	catalog catalogPage `route:"/catalog Filtered catalog"`
	sim     simAction   `route:"POST /sim/{action} Simulate"`
}

func newHandler(v *vite.Vite) (http.Handler, *app, error) {
	a := &app{store: labdata.New(), hub: live.NewHub[viewer](live.Config{}), drop: labdata.NewDropper()}
	mux := http.NewServeMux()
	if !v.Dev() {
		mux.Handle("/static/", v.StaticHandler())
	}
	if _, err := structpages.Mount(mux, pages{}, "/", "Datastar lab",
		structpages.WithArgs(a.hub, a.drop),
		structpages.WithRequestArgs(a.store.ForRequest, viewerOf),
		structpages.WithErrorHandler(errorHandler),
	); err != nil {
		return nil, nil, err
	}
	return v.Middleware(withViewer(serverTiming(mux))), a, nil
}

// app is what the tests reach into.
type app struct {
	store *store
	hub   *hub
	drop  *labdata.Dropper
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, labdata.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

const viewerCookie = "lab-viewer"

// withViewer gives a request without a viewer cookie a new viewer.
func withViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(viewerCookie); err != nil {
			c := &http.Cookie{Name: viewerCookie, Value: "viewer-" + strings.ToLower(rand.Text()[:4]), Path: "/", SameSite: http.SameSiteLaxMode}
			http.SetCookie(w, c)
			r.AddCookie(c)
		}
		next.ServeHTTP(w, r)
	})
}

func viewerOf(r *http.Request) (viewer, error) {
	c, err := r.Cookie(viewerCookie)
	if err != nil {
		return "", err
	}
	return viewer(c.Value), nil
}

type simAction struct{}

func (simAction) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, d *labdata.Dropper) error {
	id := r.FormValue("id")
	if id == "" && r.Header.Get("Content-Type") == "application/json" {
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return err
		}
		id = body.ID
	}
	publish := func(topic string, ids ...string) {
		h.Publish(context.WithoutCancel(r.Context()), everyone, topic, ids...)
	}
	action := r.PathValue("action")
	var err error
	if action == "drop-streams" {
		d.Drop(time.Second)
	} else {
		err = labdata.Simulate(s, action, id, publish)
	}
	if errors.Is(err, labdata.ErrUnknownAction) {
		return fmt.Errorf("%w: %w", err, labdata.ErrNotFound)
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func main() {
	v, err := vite.New(vite.Config{DevURL: os.Getenv("VITE_DEV_URL"), DevBase: "/__vite/", Dist: distFS, DistDir: "dist", StaticURL: "/static/"})
	if err != nil {
		log.Fatal(err)
	}
	h, _, err := newHandler(v)
	if err != nil {
		log.Fatal(err)
	}
	port := cmp.Or(os.Getenv("GO_PORT"), "7778")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: ":" + port, Handler: h, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	log.Printf("listening on http://localhost:%s", port)
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
