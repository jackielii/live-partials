package main

import (
	"context"
	"net/http"

	"github.com/jackielii/structpages"

	"github.com/jackielii/live-partials/live"
)

// Home

type homePage struct{}

component (p homePage) Page(s *store, h *hub, v viewer) {
	<Layout title="Page fragments: pushed components declared by their page" s={s} h={h} v={v} panel={<p class="text-stone-600">Open a case.</p>}>
		<div class="flex max-w-prose flex-col gap-3">
			<p>
				The hybrid stage, with each pushed component declared by its page. Case 1 is unchanged: a signal,
				and the page refetches the row. Case 2's presence dots are value frames. Cases 3 and 6 are fragment
				frames rendered through the page's own handler, as each viewer: Props is the only loader, and the
				jobs page's access check runs on every push.
			</p>
			<p>The hybrid app (port 7779) is the same cases with fragments rendered by functions beside the pages.</p>
		</div>
	</Layout>
}

// Case 1: keyed list + aggregate, signal + pull exactly as in stage 3.

type seriesPage struct{}

type seriesProps struct {
	rows []series
	sum  seriesSummary
}

func (p seriesPage) Props(r *http.Request, s *store, t structpages.RenderTarget) (seriesProps, []series, series, seriesSummary, error) {
	switch {
	case t.Is(p.Row):
		row, err := live.Load(r.Context(), t, p.Row, s.SeriesByID)
		return seriesProps{}, nil, row, seriesSummary{}, err
	case t.Is(p.Summary):
		return seriesProps{}, nil, series{}, s.Summary(), nil
	}
	rows, sum := s.Series()
	return seriesProps{rows, sum}, rows, series{}, sum, nil
}

component (p seriesPage) Page(props seriesProps, s *store, h *hub, v viewer) {
	<Layout title="Series catalog" s={s} h={h} v={v} panel={<seriesPanel/>}>
		<p.Summary sum={props.sum}/>
		<table class="w-full text-sm" data-testid="series">
			<thead><tr class="text-left text-stone-500"><th>Series</th><th>Points</th><th>Status</th></tr></thead>
			<p.Rows rows={props.rows}/>
		</table>
	</Layout>
}

component (p seriesPage) Summary(sum seriesSummary) {
	<section
		id={seriesPage.Summary |> id}
		{ live.On("lab:series")... }
		class="grid grid-cols-4 gap-2 rounded border border-stone-200 bg-white p-3 text-sm"
		data-testid="summary"
	>
		<div>Series <b data-testid="sum-count">{ sum.Count }</b></div>
		<div>Points <b data-testid="sum-points">{ sum.Points }</b></div>
		<div>Stale <b>{ sum.Stale }</b></div>
		<div>Failing <b data-testid="sum-failing">{ sum.Failing }</b></div>
	</section>
}

component (p seriesPage) Rows(rows []series) {
	<tbody id={seriesPage.Rows |> id} { live.On("lab:series")... }>
		{ for _, s := range rows {
			<p.Row s={s}/>
		} }
	</tbody>
}

component (p seriesPage) Row(s series) {
	<tr id={seriesPage.Row |> key(s.ID)} { live.On("lab:series", s.ID)... }>
		<td>{ s.Name }</td>
		<td data-testid="points">{ s.Points }</td>
		<td data-testid="status">{ s.Status }</td>
	</tr>
}

component seriesPanel() {
	<SimButton action="series-touch" id="s3">Change series s3</SimButton>
	<SimButton action="series-touch" id="s2 s5 s8">Change series s2, s5 and s8 together</SimButton>
}

// Case 2: one person in many places, as value frames. Nothing on this page
// is refetched for presence: every dot and presence label listens to the
// person's value, however many there are and wherever they sit (the
// sidebar is the layout's).

type peoplePage struct{}

// registerPeople gives the stream presence's current values, sent with
// every resync so the dots converge after missed frames.
func registerPeople(h *hub, s *store) {
	h.Current("lab:presence", func(ctx context.Context, _ viewer) (map[string]string, error) {
		return s.ForContext(ctx).AllPresence(), nil
	})
}

component (p peoplePage) Page(s *store, h *hub, v viewer) {
	{{ crowd := s.Crowd() }}
	<Layout title="People" s={s} h={h} v={v} panel={<peoplePanel/>}>
		<div class="flex gap-4">
			{ for _, id := range []string{"p1", "p2"} {
				{ if pr, err := s.Person(id); err == nil {
					<personCard pr={pr} big={true}/>
				} }
			} }
		</div>
		<section class="flex flex-wrap gap-1.5" data-testid="crowd-strip">
			{ for _, pr := range crowd {
				<presenceDot pr={pr}/>
			} }
		</section>
		<table class="w-full text-sm" data-testid="directory">
			<thead><tr class="text-left text-stone-500"><th>Name</th><th>Presence</th></tr></thead>
			<tbody>
				{ for _, pr := range append(s.People(), crowd...) {
					<tr data-testid={"row-" + pr.ID}>
						<td>{ pr.Name }</td>
						<td class="flex items-center gap-2"><presenceDot pr={pr}/><span { live.ValueText("lab:presence", pr.ID)... }>{ pr.Presence }</span></td>
					</tr>
				} }
			</tbody>
		</table>
		<section class="grid grid-cols-5 gap-2" data-testid="crowd-cards">
			{ for _, pr := range crowd {
				<personCard pr={pr} big={false}/>
			} }
		</section>
	</Layout>
}

component personCard(pr person, big bool) {
	<article class={ "rounded border border-stone-200 bg-white p-2", "w-56 p-3": big } data-testid={"card-" + pr.ID}>
		<h3 class={ "flex items-center gap-2", "font-semibold": big, "text-xs": !big }><presenceDot pr={pr}/>{ pr.Name }</h3>
		{ if big {
			<p class="text-xs text-stone-500">Currently <span { live.ValueText("lab:presence", pr.ID)... }>{ pr.Presence }</span></p>
		} }
	</article>
}

component peoplePanel() {
	<SimButton action="presence" id="p1">Ada changes presence</SimButton>
	<SimButton action="presence" id="p4">Edsger changes presence</SimButton>
	<SimButton action="presence-crowd" id="">All fifty members change presence</SimButton>
}
