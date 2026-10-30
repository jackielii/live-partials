// Command pagefrag is the hybrid stage (signal + pull, value frames,
// fragment frames on one stream) with each pushed component declared by its
// page (a Live method) and rendered through that page's own handler, as the
// subscriber: Props is the only loader and the page's middleware runs on
// every push. Case 1 stays signal + pull for contrast.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gsxhq/vite"
	"github.com/jackielii/structpages"

	"github.com/jackielii/live-partials/examples/labdata"
	"github.com/jackielii/live-partials/live"
)

// viewer is the signed-in user: here a name kept in a cookie, so two browser
// contexts are two users and per-viewer fragments are visible.
type viewer string

type hub = live.Hub[viewer]

var everyone = live.AudienceFunc[viewer](func(viewer) bool { return true })

//go:embed all:dist
var distFS embed.FS

type pages struct {
	home   homePage    `route:"/{$} Page fragments lab"`
	events eventsRoute `route:"GET /events Events"`
	series seriesPage  `route:"/series Series catalog"`
	people peoplePage  `route:"/people People"`
	drafts draftsPage  `route:"/drafts Report draft"`
	save   saveDraft   `route:"POST /drafts/{rowId} Save draft row"`
	jobs   jobsPage    `route:"/jobs Jobs"`
	sim    simAction   `route:"POST /sim/{action} Simulate"`
}

// app is what the tests reach into.
type app struct {
	store  *store
	hub    *hub
	drop   *labdata.Dropper
	access *access
	pages  *structpages.StructPages
}

// access is who has lost access to the jobs: the jobs page's middleware
// answers them 403, on a pull and on a push alike.
type access struct {
	mu      sync.Mutex
	revoked map[viewer]bool
}

func (a *access) set(v viewer, revoked bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.revoked == nil {
		a.revoked = map[viewer]bool{}
	}
	a.revoked[v] = revoked
}

func (a *access) allowed(v viewer) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.revoked[v]
}

func (a *access) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revoked = nil
}

// require is the jobs page's middleware.
func (a *access) require(next http.Handler, _ *structpages.PageNode) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, err := viewerOf(r); err != nil || !a.allowed(v) {
			http.Error(w, "no access to jobs", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// options switch the push kinds off, so the tests can measure the same
// change as signal + pull, or render the fragments with the hybrid stage's
// direct render functions (direct.go) instead of through the pages.
type options struct{ noPush, direct bool }

func newHandler(v *vite.Vite, opt options) (http.Handler, *app, error) {
	a := &app{store: labdata.New(), hub: live.NewHub[viewer](live.Config{}), drop: labdata.NewDropper(), access: &access{}}
	middlewares := []structpages.MiddlewareFunc{live.Middleware}
	switch {
	case opt.noPush:
	case opt.direct:
		registerPeople(a.hub, a.store)
		registerDirect(a.hub, a.store, a.access)
	default:
		registerPeople(a.hub, a.store)
		middlewares = append([]structpages.MiddlewareFunc{a.hub.Pages}, middlewares...)
	}
	mux := http.NewServeMux()
	if !v.Dev() {
		mux.Handle("/static/", v.StaticHandler())
	}
	sp, err := structpages.Mount(mux, pages{}, "/", "Page fragments lab",
		structpages.WithArgs(a.hub, a.drop, opt, a.access),
		structpages.WithRequestArgs(a.store.ForRequest, viewerOf),
		structpages.WithTargetSelector(structpages.HTMXv4RenderTarget),
		structpages.WithMiddlewares(middlewares...),
		structpages.WithErrorHandler(errorHandler),
	)
	if err != nil {
		return nil, nil, err
	}
	a.pages = sp
	return v.Middleware(withViewer(serverTiming(mux))), a, nil
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, labdata.ErrNotFound) || errors.Is(err, live.ErrNotFound) {
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

// eventsRoute is the tab's one stream. It is a structpages route so its
// context carries the page tree the fragments' ids are generated from.
type eventsRoute struct{}

func (eventsRoute) ServeHTTP(w http.ResponseWriter, r *http.Request, h *hub, v viewer, d *labdata.Dropper) error {
	ctx, release, ok := d.Stream(r.Context())
	defer release()
	if !ok {
		http.Error(w, "restarting", http.StatusServiceUnavailable)
		return nil
	}
	ctx, queries := labdata.WithQueries(ctx)
	if err := h.ServeSSE(&meteredStream{ResponseWriter: w, queries: queries}, r.WithContext(ctx), v); err != nil {
		slog.DebugContext(r.Context(), "stream ended", "err", err)
	}
	return nil
}

type simAction struct{}

func (simAction) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, d *labdata.Dropper, opt options, acc *access) error {
	ctx := context.WithoutCancel(r.Context())
	publish := func(topic string, ids ...string) {
		if topic == "lab:presence" && !opt.noPush {
			// Presence is a value every viewer may see: send the value itself.
			h.PublishValues(ctx, everyone, topic, s.Presence(ids))
			return
		}
		h.Publish(ctx, everyone, topic, ids...)
	}
	var err error
	switch action := r.PathValue("action"); action {
	case "drop-streams":
		d.Drop(time.Second)
	case "revoke-jobs":
		acc.set(viewer(r.FormValue("id")), true)
	case "job-gone": // a job deleted elsewhere: the store no longer has it
		publish("lab:job", r.FormValue("id"))
	default:
		if action == "reset" {
			acc.reset()
		}
		err = labdata.Simulate(s, action, r.FormValue("id"), publish)
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
	h, _, err := newHandler(v, options{})
	if err != nil {
		log.Fatal(err)
	}
	port := cmp.Or(os.Getenv("GO_PORT"), "7780")
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
