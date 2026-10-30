package main

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gsxhq/vite"
	"github.com/jackielii/structpages"

	"github.com/jackielii/live-partials/examples/labdata"
	"github.com/jackielii/live-partials/live"
)

// The lab has no sign-in: every tab is the same viewer and hears everything.
type hub = live.Hub[string]

var everyone = live.AudienceFunc[string](func(string) bool { return true })

//go:embed all:dist
var distFS embed.FS

type pages struct {
	home    homePage    `route:"/{$} Lab"`
	shell   shellPage   `route:"GET /shell Shell"`
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

func newHandler(v *vite.Vite) (http.Handler, error) {
	s := labdata.New()
	h := live.NewHub[string](live.Config{})
	d := labdata.NewDropper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		ctx, release, ok := d.Stream(r.Context())
		defer release()
		if !ok {
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
		if err := h.ServeSSE(w, r.WithContext(ctx), "tab"); err != nil {
			slog.DebugContext(r.Context(), "stream ended", "err", err)
		}
	})
	if !v.Dev() {
		mux.Handle("/static/", v.StaticHandler())
	}
	if _, err := structpages.Mount(mux, pages{}, "/", "Lab",
		structpages.WithArgs(h, d),
		structpages.WithRequestArgs(s.ForRequest),
		structpages.WithTargetSelector(structpages.HTMXv4RenderTarget),
		structpages.WithMiddlewares(live.Middleware),
		structpages.WithErrorHandler(errorHandler),
	); err != nil {
		return nil, err
	}
	return v.Middleware(serverTiming(mux)), nil
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNotFound) || errors.Is(err, live.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

type simAction struct{}

func (simAction) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, d *labdata.Dropper) error {
	publish := func(topic string, ids ...string) {
		h.Publish(context.WithoutCancel(r.Context()), everyone, topic, ids...)
	}
	var err error
	if action := r.PathValue("action"); action == "drop-streams" {
		d.Drop(time.Second)
	} else {
		err = labdata.Simulate(s, action, r.FormValue("id"), publish)
	}
	if errors.Is(err, labdata.ErrUnknownAction) {
		return fmt.Errorf("%w: %w", err, errNotFound)
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
	h, err := newHandler(v)
	if err != nil {
		log.Fatal(err)
	}
	port := cmp.Or(os.Getenv("GO_PORT"), "7777")
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
