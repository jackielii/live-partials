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
)

//go:embed all:dist
var distFS embed.FS

type pages struct {
	home       homePage      `route:"/{$} Lab"`
	series     seriesPage    `route:"/series Series catalog"`
	seriesRow  seriesRowPage `route:"GET /series/{seriesId}/row Series row"`
	people     peoplePage    `route:"/people People"`
	personPart personPart    `route:"GET /people/{personId}/{view} Person"`
	drafts     draftsPage    `route:"/drafts Report draft"`
	draftRow   draftRowPage  `route:"GET /drafts/{rowId} Draft row"`
	saveDraft  saveDraft     `route:"POST /drafts/{rowId} Save draft row"`
	files      filesPage     `route:"/files Files"`
	burst      burstPage     `route:"/burst Burst"`
	burstRow   burstRowPage  `route:"GET /burst/{rowId} Burst row"`
	jobs       jobsPage      `route:"/jobs Jobs"`
	jobPart    jobPart       `route:"GET /jobs/{jobId}/{view} Job"`
	sim        simAction     `route:"POST /sim/{action} Simulate"`
}

func newHandler(v *vite.Vite) (http.Handler, error) {
	s := newStore()
	b := newBroadcaster()
	mux := http.NewServeMux()
	mux.Handle("GET /events", b)
	if !v.Dev() {
		mux.Handle("/static/", v.StaticHandler())
	}
	if _, err := structpages.Mount(mux, pages{}, "/", "Lab",
		structpages.WithArgs(s, b),
		structpages.WithTargetSelector(structpages.HTMXv4RenderTarget),
		structpages.WithErrorHandler(errorHandler),
	); err != nil {
		return nil, err
	}
	return v.Middleware(serverTiming(mux)), nil
}

func errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	slog.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

type simAction struct{}

func (simAction) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, b *broadcaster) error {
	id := r.FormValue("id")
	var err error
	switch a := r.PathValue("action"); a {
	case "reset":
		s.reset()
		b.publish("resync")
	case "series-touch":
		if err = s.TouchSeries(id); err == nil {
			b.publish("lab:series", id)
		}
	case "presence":
		if err = s.CyclePresence(id); err == nil {
			b.publish("lab:presence", id)
		}
	case "draft":
		d, derr := s.Draft(id)
		if err = derr; err == nil {
			if err = s.SaveDraft(id, fmt.Sprint(len(d.Value)*1000+7), "Grace"); err == nil {
				b.publish("lab:draft", id)
			}
		}
	case "file-add":
		f := s.AddFile(fmt.Sprintf("upload-%d.pdf", time.Now().UnixMilli()%100000))
		b.publish("lab:files", f.ID)
	case "file-delete":
		if err = s.DeleteFile(id); err == nil {
			b.publish("lab:files", id)
		}
	case "burst":
		go func() {
			for _, row := range s.Burst()[:50] {
				if s.BumpBurst(row.ID) == nil {
					b.publish("lab:burst", row.ID)
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
	case "job-advance":
		if err = s.AdvanceJob(id); err == nil {
			b.publish("lab:job-progress", id)
		}
	case "job-step":
		if err = s.StepJob(id); err == nil {
			b.publish("lab:job", id)
		}
	default:
		return fmt.Errorf("unknown action %q: %w", a, errNotFound)
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
