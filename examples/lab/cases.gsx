package main

import (
	"net/http"

	"github.com/jackielii/live-partials/live"
	"github.com/jackielii/structpages"
)

// Home

type homePage struct{}

component (p homePage) Page(s *store, h *hub) {
	<Layout title="Six cases" s={s} h={h} panel={<p class="text-stone-600">Open a case.</p>}>
		<p class="max-w-prose">
			Each case is modelled on a real screen. The amber panel changes data as another user would, the
			server publishes a frame on the stream, and the page updates itself. The meter counts what this tab
			spent.
		</p>
	</Layout>
}

// Shell: the layout's own live parts are rendered by this route, so no page
// has to recognise them in its Props.

type shellPage struct{}

func (p shellPage) Props(r *http.Request, s *store) (person, error) {
	return s.Person(live.Key(r))
}

component (p shellPage) Page(pr person) {
	<p.DM pr={pr}/>
}

component (p shellPage) DM(pr person) {
	<li
		id={shellPage.DM |> kid(pr.ID)}
		{ live.On("lab:presence", pr.ID)... }
		data-live-url={shellPage{} |> url}
		class="flex items-center gap-2"
	><presenceDot state={pr.Presence}/>{ pr.Name }</li>
}

// Case 1: keyed list + aggregate

type seriesPage struct{}

type seriesProps struct {
	rows []series
	sum  seriesSummary
}

func (p seriesPage) Props(r *http.Request, s *store, t structpages.RenderTarget) (seriesProps, []series, series, seriesSummary, error) {
	switch {
	case t.Is(p.Row):
		row, err := s.OneSeries(live.Key(r))
		return seriesProps{}, nil, row, seriesSummary{}, err
	case t.Is(p.Summary):
		return seriesProps{}, nil, series{}, s.Summary(), nil
	}
	rows, sum := s.Series()
	return seriesProps{rows, sum}, rows, series{}, sum, nil
}

component (p seriesPage) Page(props seriesProps, s *store, h *hub) {
	<Layout title="Series catalog" s={s} h={h} panel={<seriesPanel/>}>
		<p.Content props={props}/>
	</Layout>
}

component (p seriesPage) Content(props seriesProps) {
	<p.Summary sum={props.sum}/>
	<table class="w-full text-sm" data-testid="series">
		<thead><tr class="text-left text-stone-500"><th>Series</th><th>Points</th><th>Status</th></tr></thead>
		<p.Rows rows={props.rows}/>
	</table>
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
	<tr id={seriesPage.Row |> kid(s.ID)} { live.On("lab:series", s.ID)... }>
		<td>{ s.Name }</td>
		<td data-testid="points">{ s.Points }</td>
		<td data-testid="status">{ s.Status }</td>
	</tr>
}

component seriesPanel() {
	<SimButton action="series-touch" id="s3">Change series s3</SimButton>
	<SimButton action="series-touch" id="s7">Change series s7</SimButton>
}

// Case 2: one person in many places

type peoplePage struct{}

func (p peoplePage) Props(r *http.Request, s *store, t structpages.RenderTarget) (person, error) {
	if t.Is(p.Row) || t.Is(p.Card) {
		return s.Person(live.Key(r))
	}
	return person{}, nil
}

component (p peoplePage) Page(s *store, h *hub) {
	<Layout title="People" s={s} h={h} panel={<peoplePanel/>}>
		<div class="flex gap-4">
			{ for _, id := range []string{"p1", "p2"} {
				{ if pr, err := s.Person(id); err == nil {
					<p.Card pr={pr}/>
				} }
			} }
		</div>
		<table class="w-full text-sm" data-testid="directory">
			<thead><tr class="text-left text-stone-500"><th>Name</th><th>Presence</th></tr></thead>
			<tbody>
				{ for _, pr := range s.People() {
					<p.Row pr={pr}/>
				} }
			</tbody>
		</table>
	</Layout>
}

component (p peoplePage) Row(pr person) {
	<tr id={peoplePage.Row |> kid(pr.ID)} { live.On("lab:presence", pr.ID)... }>
		<td>{ pr.Name }</td>
		<td class="flex items-center gap-2"><presenceDot state={pr.Presence}/>{ pr.Presence }</td>
	</tr>
}

component (p peoplePage) Card(pr person) {
	<article
		id={peoplePage.Card |> kid(pr.ID)}
		{ live.On("lab:presence", pr.ID)... }
		class="w-56 rounded border border-stone-200 bg-white p-3"
	>
		<h3 class="flex items-center gap-2 font-semibold"><presenceDot state={pr.Presence}/>{ pr.Name }</h3>
		<p class="text-xs text-stone-500">Currently { pr.Presence }</p>
	</article>
}

component peoplePanel() {
	<SimButton action="presence" id="p1">Ada changes presence</SimButton>
	<SimButton action="presence" id="p4">Edsger changes presence</SimButton>
}
