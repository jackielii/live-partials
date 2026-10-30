package labdata

import (
	"context"
	"sync"
	"time"
)

// Dropper simulates a server restart for the streams: Drop ends every open
// stream and refuses new ones for a while.
type Dropper struct {
	mu      sync.Mutex
	cancels map[*context.CancelFunc]struct{}
	until   time.Time
}

func NewDropper() *Dropper {
	return &Dropper{cancels: map[*context.CancelFunc]struct{}{}}
}

// Stream returns a context that Drop cancels and a release to call when the
// stream ends. ok is false while streams are refused; the caller answers 503.
func (d *Dropper) Stream(ctx context.Context) (_ context.Context, release func(), ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Now().Before(d.until) {
		return ctx, func() {}, false
	}
	ctx, cancel := context.WithCancel(ctx)
	d.cancels[&cancel] = struct{}{}
	return ctx, func() {
		d.mu.Lock()
		delete(d.cancels, &cancel)
		d.mu.Unlock()
		cancel()
	}, true
}

// Drop ends every open stream and refuses new ones for refuse.
func (d *Dropper) Drop(refuse time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.until = time.Now().Add(refuse)
	for c := range d.cancels {
		(*c)()
	}
}
