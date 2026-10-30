package main

import (
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
