package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type frame struct {
	topic string
	ids   []string
}

// broadcaster fans every published frame out to every connected tab.
type broadcaster struct {
	mu   sync.Mutex
	subs map[chan frame]struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: map[chan frame]struct{}{}}
}

func (b *broadcaster) publish(topic string, ids ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- frame{topic, ids}:
		default:
		}
	}
}

func (b *broadcaster) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ch := make(chan frame, 256)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	rc := http.NewResponseController(w)
	fmt.Fprint(w, ": connected\n\nevent: resync\ndata: {}\n\n")
	rc.Flush()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case f := <-ch:
			data, _ := json.Marshal(map[string][]string{"ids": f.ids})
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", f.topic, data)
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
