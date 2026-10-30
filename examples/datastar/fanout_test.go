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

	"github.com/andybalholm/brotli"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/jackielii/live-partials/examples/internal/fanout"
)

// TestFanOut measures case 7 for push-HTML: N viewers on the series page,
// each with its own stream, and one changed row. Every stream renders the
// row and the summary for its viewer and writes them; no tab makes a
// request. The brotli variant compresses each stream as one long response
// (datastar.WithCompression) and counts the compressed bytes read.
func TestFanOut(t *testing.T) {
	for _, tc := range []struct {
		stage string
		opts  []datastar.CompressionOption
	}{
		{"datastar", nil},
		{"datastar-brotli", []datastar.CompressionOption{datastar.WithBrotli()}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			streamCompression = tc.opts
			defer func() { streamCompression = nil }()
			fanout.Report(t, fanOut(t, tc.stage, fanout.N(), tc.opts != nil))
		})
	}
}

func fanOut(t *testing.T, stage string, n int, compressed bool) fanout.Result {
	h, a := labHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	defer srv.CloseClientConnections() // the streams never end on their own
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 2 * n, DisableCompression: true}}
	since := fanout.Since(t, client, srv.URL+"/series")

	subscribe := func(i int) (func() (fanout.Sample, error), error) {
		req, _ := http.NewRequest("GET", srv.URL+"/series/updates?since="+url.QueryEscape(since), nil)
		req.AddCookie(&http.Cookie{Name: viewerCookie, Value: "viewer-" + strconv.Itoa(i)})
		req.Header.Set("Datastar-Request", "true")
		if compressed {
			req.Header.Set("Accept-Encoding", "br")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		wire := &fanout.CountingReader{R: resp.Body}
		var body io.Reader = wire
		if compressed {
			if enc := resp.Header.Get("Content-Encoding"); enc != "br" {
				return nil, fmt.Errorf("Content-Encoding %q", enc)
			}
			body = brotli.NewReader(wire)
		}
		events := fanout.NewReader(body)
		return func() (fanout.Sample, error) {
			var s fanout.Sample
			before := wire.N
			for {
				e, err := events.Next()
				if err != nil {
					return s, err
				}
				switch {
				case e.Name == "lab-meter":
					f := strings.Fields(e.Data) // server <ms> queries <n>
					s.ServerMS, _ = strconv.ParseFloat(f[1], 64)
					s.Queries, _ = strconv.Atoi(f[3])
					s.Updated = time.Now()
					if compressed {
						s.Brotli = wire.N - before
					}
					if s.Renders != 2 {
						return s, fmt.Errorf("%d patches, want 2", s.Renders)
					}
					return s, nil
				case strings.HasPrefix(e.Name, "datastar-"):
					s.Bytes += int64(e.Size)
					s.Renders++
				}
			}
		}, nil
	}
	q0 := a.store.TotalQueries()
	change := func() error {
		resp, err := client.PostForm(srv.URL+"/sim/series-touch", url.Values{"id": {"s3"}})
		if err == nil && resp.StatusCode != http.StatusNoContent {
			err = fmt.Errorf("change: %s", resp.Status)
		}
		return err
	}
	const rounds = 7
	res := fanout.Measure(t, stage, n, rounds, subscribe, change)
	if got := a.store.TotalQueries() - q0; got != rounds*(res.Queries+1) { // +1: the change itself
		t.Errorf("store counted %d queries over %d rounds, streams reported %d per round", got, rounds, res.Queries)
	}
	return res
}
