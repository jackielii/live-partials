package main

import (
	"context"
	"errors"

	"github.com/jackielii/live-partials/live"
)

var errNoAccess = errors.New("no access to jobs")

// registerDirect registers the hybrid stage's render functions, beside the
// pages, for measuring them against rendering through the pages
// (options.direct). Each repeats its page's loading and checks.
func registerDirect(h *hub, s *store, a *access) {
	h.Fragment("lab:draft", draftsPage.Row, func(ctx context.Context, v viewer, key string) (live.Component, error) {
		d, err := viewDraft(s.ForContext(ctx), v, key)
		if err != nil {
			return nil, err
		}
		return draftsPage{}.Row(d), nil
	})
	render := func(c func(job) live.Component) live.FragmentFunc[viewer] {
		return func(ctx context.Context, v viewer, key string) (live.Component, error) {
			if !a.allowed(v) {
				return nil, errNoAccess
			}
			j, err := s.ForContext(ctx).Job(key)
			if err != nil {
				return nil, err
			}
			return c(j), nil
		}
	}
	h.Fragment("lab:job", jobsPage.Card, render(func(j job) live.Component { return jobsPage{}.Card(j) }))
	h.Fragment("lab:job-progress", jobsPage.Progress, render(func(j job) live.Component { return jobsPage{}.Progress(j) }))
}
