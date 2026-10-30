package main

import (
	"net/http"

	"github.com/jackielii/structpages"

	"github.com/jackielii/live-partials/live"
)

// Case 8: URL state. The filter lives in the URL (?status=); the filter links
// are boosted, so htmx pushes the URL and Back/Forward restore it. Live
// refetches go to location.pathname + location.search, so every refetch
// carries the filter: a row that leaves the filter answers 404 and is
// removed, a row that enters it is an unknown id and refetches the list.

type catalogPage struct{}

type catalogProps struct {
	status string
	rows   []series
}

var statuses = []string{"", "ok", "stale", "failing"}

func (p catalogPage) Props(r *http.Request, s *store, t structpages.RenderTarget) (catalogProps, []series, series, error) {
	status := r.URL.Query().Get("status")
	if t.Is(p.Row) {
		row, err := live.Load(r.Context(), t, p.Row, func(ids []string) (map[string]series, error) {
			rows, err := s.SeriesByID(ids)
			for id, x := range rows {
				if status != "" && x.Status != status {
					delete(rows, id)
				}
			}
			return rows, err
		})
		return catalogProps{}, nil, row, err
	}
	rows := s.Catalog(status)
	return catalogProps{status, rows}, rows, series{}, nil
}

component (p catalogPage) Page(props catalogProps, s *store, h *hub) {
	<Layout title="Filtered catalog" s={s} h={h} panel={<catalogPanel/>}>
		<nav class="flex gap-2 text-sm" data-testid="filters" hx-boost:inherited="true">
			{ for _, st := range statuses {
				<a
					{ if st == "" { href={catalogPage{} |> url} } else { href=f`@{catalogPage{} |> url}?status=@{st}` } }
					class={ "rounded border border-stone-300 px-2 py-1", "bg-stone-800 text-white": st == props.status }
				>{ if st == "" { all } else { { st } } }</a>
			} }
		</nav>
		<p.Count props={props}/>
		<p.List rows={props.rows}/>
	</Layout>
}

component (p catalogPage) Count(props catalogProps) {
	<p id={catalogPage.Count |> id} { live.On("lab:series")... } class="text-sm text-stone-500">
		<span data-testid="catalog-count">{ len(props.rows) }</span> series{ if props.status != "" { {" "}with status { props.status } } }
	</p>
}

component (p catalogPage) List(rows []series) {
	<table id={catalogPage.List |> id} { live.On("lab:series")... } data-live-insert class="w-full text-sm" data-testid="catalog">
		<tbody>
			{ for _, s := range rows {
				<p.Row s={s}/>
			} }
		</tbody>
	</table>
}

component (p catalogPage) Row(s series) {
	<tr id={catalogPage.Row |> key(s.ID)} { live.On("lab:series", s.ID)... }>
		<td>{ s.Name }</td>
		<td data-testid="status">{ s.Status }</td>
	</tr>
}

component catalogPanel() {
	<SimButton action="series-touch" id="s3">Change series s3</SimButton>
	<SimButton action="series-touch" id="s7">Change series s7</SimButton>
}
