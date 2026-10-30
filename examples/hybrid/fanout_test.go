package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackielii/live-partials/examples/internal/fanout"
	"github.com/jackielii/live-partials/live"
)

// TestFanOut measures case 7 for the hybrid's frame kinds: one change seen
// by N tabs.
//
//   - values: a presence change, sent as a value frame; no tab requests or
//     renders anything.
//   - fragments-wide: a job's status change, rendered on every subscriber's
//     stream for its viewer.
//   - fragments-single: the same change with an audience of one viewer (a
//     job only its owner watches) while N streams are open: one render.
//   - pull-jobs: the same app without push, so the job card is a signal and
//     every tab refetches it, as in stage 3.
func TestFanOut(t *testing.T) {
	n := fanout.N()
	t.Run("values", func(t *testing.T) {
		srv, client, at := fanOutServer(t, options{}, n, "/people")
		res := fanout.Measure(t, "hybrid-values", n, 7, pushSubscriber(srv, client, at, "lab:presence", 0), simulate(client, srv, "presence", "p1"))
		fanout.Report(t, res)
	})
	t.Run("fragments-wide", func(t *testing.T) {
		srv, client, at := fanOutServer(t, options{}, n, "/jobs")
		res := fanout.Measure(t, "hybrid-fragments-wide", n, 7, pushSubscriber(srv, client, at, "lab:job", 1), simulate(client, srv, "job-step", "j1"))
		fanout.Report(t, res)
	})
	t.Run("fragments-single", func(t *testing.T) {
		srv, client, at := fanOutServer(t, options{}, n, "/jobs")
		a := apps[srv]
		var leaked atomic.Int32
		for i := 1; i < n; i++ { // the bystanders: subscribed, not in the audience
			resp, err := streamRequest(client, srv, at, viewer("viewer-"+strconv.Itoa(i)))
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				events := fanout.NewReader(resp.Body)
				for {
					e, err := events.Next()
					if err != nil {
						return
					}
					if e.Name == "lab:job" {
						leaked.Add(1)
					}
				}
			}()
		}
		owner := live.AudienceFunc[viewer](func(v viewer) bool { return v == "viewer-0" })
		change := func() error {
			if err := a.store.StepJob("j1"); err != nil {
				return err
			}
			a.hub.Publish(context.Background(), owner, "lab:job", "j1")
			return nil
		}
		res := fanout.Measure(t, "hybrid-fragments-single", 1, 7, pushSubscriber(srv, client, at, "lab:job", 1), change)
		res.Subscribers = n
		fanout.Report(t, res)
		if leaked.Load() != 0 {
			t.Errorf("%d frames reached viewers outside the audience", leaked.Load())
		}
	})
	t.Run("pull-jobs", func(t *testing.T) {
		srv, client, at := fanOutServer(t, options{noPush: true}, n, "/jobs")
		res := fanout.Measure(t, "hybrid-pull-jobs", n, 7, pullSubscriber(srv, client, at), simulate(client, srv, "job-step", "j1"))
		fanout.Report(t, res)
	})
}

var apps = map[*httptest.Server]*app{}

func fanOutServer(t *testing.T, opt options, n int, page string) (*httptest.Server, *http.Client, string) {
	srv, a := newServer(t, opt)
	apps[srv] = a
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 2 * n}}
	t.Cleanup(client.CloseIdleConnections)
	return srv, client, fanout.Since(t, client, srv.URL+page)
}

func streamRequest(client *http.Client, srv *httptest.Server, since string, v viewer) (*http.Response, error) {
	req, _ := http.NewRequest("GET", srv.URL+"/events?since="+url.QueryEscape(since), nil)
	req.AddCookie(&http.Cookie{Name: viewerCookie, Value: string(v)})
	return client.Do(req)
}

func simulate(client *http.Client, srv *httptest.Server, action, id string) func() error {
	return func() error {
		resp, err := client.PostForm(srv.URL+"/sim/"+action, url.Values{"id": {id}})
		if err == nil && resp.StatusCode != http.StatusNoContent {
			err = fmt.Errorf("change: %s", resp.Status)
		}
		return err
	}
}

// pushSubscriber is a tab that applies what its stream pushes: the frame of
// topic carries the update (renders fragments), and the stream's lab-meter
// event after it reports the stream's render time and queries.
func pushSubscriber(srv *httptest.Server, client *http.Client, since, topic string, renders int) fanout.Subscribe {
	return func(i int) (func() (fanout.Sample, error), error) {
		resp, err := streamRequest(client, srv, since, viewer("viewer-"+strconv.Itoa(i)))
		if err != nil {
			return nil, err
		}
		events := fanout.NewReader(resp.Body)
		return func() (fanout.Sample, error) {
			var s fanout.Sample
			for {
				e, err := events.Next()
				if err != nil {
					return s, err
				}
				switch e.Name {
				case topic:
					s.Updated = time.Now()
					s.Bytes += int64(e.Size)
					s.Brotli += fanout.BrotliSize([]byte(e.Data))
					s.Renders = strings.Count(e.Data, `"target":`)
					if s.Renders != renders {
						return s, fmt.Errorf("%d fragments, want %d: %s", s.Renders, renders, e.Data)
					}
				case "lab-meter":
					if s.Updated.IsZero() {
						continue
					}
					f := strings.Fields(e.Data) // server <ms> queries <n>
					s.ServerMS, _ = strconv.ParseFloat(f[1], 64)
					s.Queries, _ = strconv.Atoi(f[3])
					return s, nil
				}
			}
		}, nil
	}
}

// pullSubscriber is a stage 3 tab on the jobs page: a lab:job frame for j1
// makes js/live.js refetch the card in one batch GET.
func pullSubscriber(srv *httptest.Server, client *http.Client, since string) fanout.Subscribe {
	return func(i int) (func() (fanout.Sample, error), error) {
		v := viewer("viewer-" + strconv.Itoa(i))
		resp, err := streamRequest(client, srv, since, v)
		if err != nil {
			return nil, err
		}
		events := fanout.NewReader(resp.Body)
		return func() (fanout.Sample, error) {
			for {
				e, err := events.Next()
				if err != nil {
					return fanout.Sample{}, err
				}
				if e.Name != "lab:job" {
					continue
				}
				req, _ := http.NewRequest("GET", srv.URL+"/jobs", nil)
				req.Header.Set("HX-Request", "true")
				req.Header.Set(live.TargetsHeader, "jobs-card--j1")
				req.AddCookie(&http.Cookie{Name: viewerCookie, Value: string(v)})
				r, err := client.Do(req)
				if err != nil {
					return fanout.Sample{}, err
				}
				b, err := io.ReadAll(r.Body)
				r.Body.Close()
				if err != nil {
					return fanout.Sample{}, err
				}
				if strings.Count(string(b), "<hx-partial") != 1 {
					return fanout.Sample{}, fmt.Errorf("pull: %s", b)
				}
				q, _ := strconv.Atoi(r.Header.Get("X-Queries"))
				return fanout.Sample{
					Requests: 1,
					Bytes:    int64(e.Size + len(b)),
					Brotli:   int64(e.Size) + fanout.BrotliSize(b),
					Queries:  q,
					Renders:  1,
					ServerMS: fanout.ServerTiming(r),
					Updated:  time.Now(),
				}, nil
			}
		}, nil
	}
}
