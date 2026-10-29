package main

import "net/http"

// Home

type homePage struct{}

component (p homePage) Page(s *store) {
	<Layout title="Six cases" s={s} panel={<p class="text-stone-600">Open a case.</p>}>
		<p class="max-w-prose">
			Each case is modelled on a real screen. The amber panel changes data as another user would, the
			server publishes a frame on the stream, and the page updates itself. The meter counts what this tab
			spent.
		</p>
	</Layout>
}

// Case 1: keyed list + aggregate

type seriesPage struct{}

type seriesProps struct {
	rows []series
	sum  seriesSummary
}

func (p seriesPage) Props(s *store) seriesProps {
	rows, sum := s.Series()
	return seriesProps{rows, sum}
}

component (p seriesPage) Page(props seriesProps, s *store) {
	<Layout title="Series catalog" s={s} panel={<seriesPanel/>}>
		<p.Content props={props}/>
	</Layout>
}

component (p seriesPage) Content(props seriesProps) {
	<p.Summary props={props}/>
	<table class="w-full text-sm" data-testid="series">
		<thead><tr class="text-left text-stone-500"><th>Series</th><th>Points</th><th>Status</th></tr></thead>
		<tbody>
			{ for _, s := range props.rows {
				<SeriesRow s={s}/>
			} }
		</tbody>
	</table>
}

component (p seriesPage) Summary(props seriesProps) {
	<section
		id={seriesPage.Summary |> id}
		class="grid grid-cols-4 gap-2 rounded border border-stone-200 bg-white p-3 text-sm"
		data-testid="summary"
		hx-get={seriesPage{} |> url}
		hx-trigger="lab:series from:#stream, resync from:#stream"
		hx-swap="outerMorph"
	>
		<div>Series <b data-testid="sum-count">{ props.sum.Count }</b></div>
		<div>Points <b data-testid="sum-points">{ props.sum.Points }</b></div>
		<div>Stale <b>{ props.sum.Stale }</b></div>
		<div>Failing <b data-testid="sum-failing">{ props.sum.Failing }</b></div>
	</section>
}

component SeriesRow(s series) {
	<tr
		id={"series-" + s.ID}
		data-key={s.ID}
		hx-get={seriesRowPage{} |> url(map[string]any{"seriesId": s.ID})}
		hx-trigger="lab:series[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
	>
		<td>{ s.Name }</td>
		<td data-testid="points">{ s.Points }</td>
		<td data-testid="status">{ s.Status }</td>
	</tr>
}

type seriesRowPage struct{}

func (seriesRowPage) Props(r *http.Request, s *store) (series, error) {
	return s.OneSeries(r.PathValue("seriesId"))
}

component (p seriesRowPage) Page(s series) {
	<SeriesRow s={s}/>
}

component seriesPanel() {
	<SimButton action="series-touch" id="s3">Change series s3</SimButton>
	<SimButton action="series-touch" id="s7">Change series s7</SimButton>
}

// Case 2: one person in many places

type peoplePage struct{}

component (p peoplePage) Page(s *store) {
	<Layout title="People" s={s} panel={<peoplePanel/>}>
		<div class="flex gap-4">
			{ for _, id := range []string{"p1", "p2"} {
				{ if person, err := s.Person(id); err == nil {
					<PersonCard p={person}/>
				} }
			} }
		</div>
		<table class="w-full text-sm" data-testid="directory">
			<thead><tr class="text-left text-stone-500"><th>Name</th><th>Presence</th></tr></thead>
			<tbody>
				{ for _, person := range s.People() {
					<DirectoryRow p={person}/>
				} }
			</tbody>
		</table>
	</Layout>
}

component SidebarPerson(p person) {
	<li
		id={"dm-" + p.ID}
		data-key={p.ID}
		class="flex items-center gap-2"
		hx-get={personPart{} |> url(map[string]any{"personId": p.ID, "view": "dm"})}
		hx-trigger="lab:presence[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
	><presenceDot state={p.Presence}/>{ p.Name }</li>
}

component DirectoryRow(p person) {
	<tr
		id={"dir-" + p.ID}
		data-key={p.ID}
		hx-get={personPart{} |> url(map[string]any{"personId": p.ID, "view": "row"})}
		hx-trigger="lab:presence[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
	>
		<td>{ p.Name }</td>
		<td class="flex items-center gap-2"><presenceDot state={p.Presence}/>{ p.Presence }</td>
	</tr>
}

component PersonCard(p person) {
	<article
		id={"card-" + p.ID}
		data-key={p.ID}
		class="w-56 rounded border border-stone-200 bg-white p-3"
		hx-get={personPart{} |> url(map[string]any{"personId": p.ID, "view": "card"})}
		hx-trigger="lab:presence[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
	>
		<h3 class="flex items-center gap-2 font-semibold"><presenceDot state={p.Presence}/>{ p.Name }</h3>
		<p class="text-xs text-stone-500">Currently { p.Presence }</p>
	</article>
}

type personPart struct{}

func (personPart) Props(r *http.Request, s *store) (person, error) {
	return s.Person(r.PathValue("personId"))
}

component (pp personPart) Page(p person, r *http.Request) {
	{ switch r.PathValue("view") {
	case "dm":
		<SidebarPerson p={p}/>
	case "row":
		<DirectoryRow p={p}/>
	default:
		<PersonCard p={p}/>
	} }
}

component peoplePanel() {
	<SimButton action="presence" id="p1">Ada changes presence</SimButton>
	<SimButton action="presence" id="p4">Edsger changes presence</SimButton>
}
