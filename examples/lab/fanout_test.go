package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackielii/live-partials/examples/internal/fanout"
)

// TestFanOut measures case 7 for signal + pull: N tabs on the series page
// hear one frame for a changed row, and each refetches the row and the
// summary in one batch GET, as js/live.js does. Bytes are the frame plus
// the response; brotli compresses each response on its own (a frame is too
// small to gain).
func TestFanOut(t *testing.T) {
	n := fanout.N()
	srv := httptest.NewServer(labHandler(t))
	defer srv.Close()
	defer srv.CloseClientConnections() // the streams never end on their own
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 2 * n}}
	since := fanout.Since(t, client, srv.URL+"/series")

	subscribe := func(int) (func() (fanout.Sample, error), error) {
		resp, err := client.Get(srv.URL + "/events?since=" + url.QueryEscape(since))
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
				if e.Name == "lab:series" {
					return refetch(client, srv.URL, e.Size)
				}
			}
		}, nil
	}
	change := func() error {
		resp, err := client.PostForm(srv.URL+"/sim/series-touch", url.Values{"id": {"s3"}})
		if err == nil && resp.StatusCode != http.StatusNoContent {
			err = fmt.Errorf("change: %s", resp.Status)
		}
		return err
	}
	fanout.Report(t, fanout.Measure(t, "structpages-keys", n, 7, subscribe, change))
}

// TestFanOutPresence measures case 2's change for signal + pull, to compare
// with the hybrid's value frames: N tabs on the people page hear Ada's
// presence frame, and each refetches her directory row and card from
// /people and her sidebar row from /shell, as js/live.js does (one GET per
// page URL, sent together).
func TestFanOutPresence(t *testing.T) {
	n := fanout.N()
	srv := httptest.NewServer(labHandler(t))
	defer srv.Close()
	defer srv.CloseClientConnections()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 4 * n}}
	since := fanout.Since(t, client, srv.URL+"/people")

	get := func(path, targets string) (fanout.Sample, error) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("HX-Request", "true")
		req.Header.Set("X-Live-Targets", targets)
		r, err := client.Do(req)
		if err != nil {
			return fanout.Sample{}, err
		}
		b, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			return fanout.Sample{}, err
		}
		want := len(strings.Fields(targets))
		if got := strings.Count(string(b), "<hx-partial"); got != want {
			return fanout.Sample{}, fmt.Errorf("%d partials, want %d: %s", got, want, b)
		}
		q, _ := strconv.Atoi(r.Header.Get("X-Queries"))
		return fanout.Sample{Requests: 1, Bytes: int64(len(b)), Brotli: fanout.BrotliSize(b), Queries: q, Renders: want, ServerMS: fanout.ServerTiming(r)}, nil
	}
	subscribe := func(int) (func() (fanout.Sample, error), error) {
		resp, err := client.Get(srv.URL + "/events?since=" + url.QueryEscape(since))
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
				if e.Name != "lab:presence" {
					continue
				}
				var page, shell fanout.Sample
				var perr, serr error
				done := make(chan struct{})
				go func() { shell, serr = get("/shell", "shell-dm--p1"); close(done) }()
				page, perr = get("/people", "people-row--p1 people-card--p1")
				<-done
				if err := errors.Join(perr, serr); err != nil {
					return fanout.Sample{}, err
				}
				return fanout.Sample{
					Requests: 2,
					Bytes:    int64(e.Size) + page.Bytes + shell.Bytes,
					Brotli:   int64(e.Size) + page.Brotli + shell.Brotli,
					Queries:  page.Queries + shell.Queries,
					Renders:  page.Renders + shell.Renders,
					ServerMS: page.ServerMS + shell.ServerMS,
					Updated:  time.Now(),
				}, nil
			}
		}, nil
	}
	change := func() error {
		resp, err := client.PostForm(srv.URL+"/sim/presence", url.Values{"id": {"p1"}})
		if err == nil && resp.StatusCode != http.StatusNoContent {
			err = fmt.Errorf("change: %s", resp.Status)
		}
		return err
	}
	fanout.Report(t, fanout.Measure(t, "structpages-keys-presence", n, 7, subscribe, change))
}

// refetch is what the dispatcher does for one lab:series frame naming s3
// on the series page.
func refetch(client *http.Client, base string, frame int) (fanout.Sample, error) {
	req, _ := http.NewRequest("GET", base+"/series", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-Live-Targets", "series-row--s3 series-summary")
	r, err := client.Do(req)
	if err != nil {
		return fanout.Sample{}, err
	}
	b, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return fanout.Sample{}, err
	}
	if n := strings.Count(string(b), "<hx-partial"); n != 2 {
		return fanout.Sample{}, fmt.Errorf("%d partials: %s", n, b)
	}
	q, _ := strconv.Atoi(r.Header.Get("X-Queries"))
	return fanout.Sample{
		Requests: 1,
		Bytes:    int64(frame + len(b)),
		Brotli:   int64(frame) + fanout.BrotliSize(b),
		Queries:  q,
		Renders:  2,
		ServerMS: fanout.ServerTiming(r),
		Updated:  time.Now(),
	}, nil
}
