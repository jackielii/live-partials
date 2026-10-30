package main

import (
	"errors"
	"net/http"

	"github.com/jackielii/structpages"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/jackielii/live-partials/live"
)

// Case 4: insert and delete. The stream cannot know whether the tab already
// shows a file id it hears about, so an existing id morphs the whole list
// (insertions land in order, unchanged rows are kept) and a missing id
// removes its row.

type filesPage struct {
	updates filesUpdates `route:"GET /updates File updates"`
}

component (p filesPage) Page(s *store, h *hub) {
	{{ files := s.Files() }}
	<Layout title="Files" stream={filesUpdates{} |> url} s={s} h={h} panel={<filesPanel/>}>
		<p.Count n={len(files)}/>
		<p.List files={files}/>
	</Layout>
}

component (p filesPage) Count(n int) {
	<p id={filesPage.Count |> id} class="text-sm text-stone-500">
		<span data-testid="file-count">{ n }</span> files
	</p>
}

component (p filesPage) List(files []file) {
	<ul id={filesPage.List |> id} class="flex flex-col divide-y divide-stone-200 rounded border border-stone-200 bg-white" data-testid="files">
		{ for _, f := range files {
			<p.Item f={f}/>
		} }
	</ul>
}

component (p filesPage) Item(f file) {
	<li id={filesPage.Item |> key(f.ID)} class="flex justify-between px-3 py-1 text-sm">
		<span>{ f.Name }</span><span class="text-stone-500">{ f.Size / 1024 } KB</span>
	</li>
}

type filesUpdates struct{}

func (filesUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	p := filesPage{}
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		if f.Topic != resync && f.Topic != "lab:files" {
			return nil
		}
		list := f.IDs == nil
		for _, id := range f.IDs {
			_, err := x.store.File(id)
			switch {
			case errors.Is(err, errNotFound):
				id, err := structpages.Key(x.ctx, p.Item, id)
				if err != nil {
					return err
				}
				x.remove(id)
			case err != nil:
				return err
			default:
				list = true
			}
		}
		files := x.store.Files()
		if list {
			if err := x.patch(p.List(files)); err != nil {
				return err
			}
		}
		return x.patch(p.Count(len(files)))
	})
}

component filesPanel() {
	<SimButton action="file-add" id="">Someone uploads a file</SimButton>
	<SimButton action="file-delete" id="f2">Someone deletes minutes.docx</SimButton>
}

// Case 5: burst. The hub coalesces the burst into two frames, as in the htmx
// stages; an id-less frame morphs the table.

type burstPage struct {
	updates burstUpdates `route:"GET /updates Burst updates"`
}

component (p burstPage) Page(s *store, h *hub) {
	<Layout title="Burst" stream={burstUpdates{} |> url} s={s} h={h} panel={<SimButton action="burst" id="">50 rows change within 100 ms</SimButton>}>
		<table class="w-full text-sm" data-testid="burst">
			<p.Rows rows={s.Burst()}/>
		</table>
	</Layout>
}

component (p burstPage) Rows(rows []burstRow) {
	<tbody id={burstPage.Rows |> id}>
		{ for _, b := range rows {
			<p.Row b={b}/>
		} }
	</tbody>
}

component (p burstPage) Row(b burstRow) {
	<tr id={burstPage.Row |> key(b.ID)}>
		<td>{ b.ID }</td>
		<td data-testid="value">{ b.Value }</td>
	</tr>
}

type burstUpdates struct{}

func (burstUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	p := burstPage{}
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		switch {
		case f.Topic == resync || f.Topic == "lab:burst" && f.IDs == nil:
			return x.patch(p.Rows(x.store.Burst()))
		case f.Topic == "lab:burst":
			for _, id := range f.IDs {
				if b, err := x.store.BurstRow(id); err == nil {
					if err := x.patch(p.Row(b)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// Case 6: nested levels. The server knows a status change re-renders the
// card, progress bar included, so it sends the card alone.

type jobsPage struct {
	updates jobsUpdates `route:"GET /updates Job updates"`
}

component (p jobsPage) Page(s *store, h *hub) {
	<Layout title="Jobs" stream={jobsUpdates{} |> url} s={s} h={h} panel={<jobsPanel/>}>
		<div class="flex flex-col gap-3">
			{ for _, j := range s.Jobs() {
				<p.Card j={j}/>
			} }
		</div>
	</Layout>
}

component (p jobsPage) Card(j job) {
	<article id={jobsPage.Card |> key(j.ID)} data-testid=f`job-@{j.ID}` class="rounded border border-stone-200 bg-white p-3">
		<h3 class="font-semibold">{ j.Name } <span class="text-xs text-stone-500" data-testid="job-status">{ j.Status }</span></h3>
		<p.Progress j={j}/>
		<p class="text-xs text-stone-500" data-testid="job-step">{ j.Step }</p>
	</article>
}

component (p jobsPage) Progress(j job) {
	<div id={jobsPage.Progress |> key(j.ID)} class="my-2 h-2 rounded bg-stone-200">
		<div class="h-2 rounded bg-sky-500" style=css`width: @{j.Progress}%` data-testid="job-progress" data-progress={j.Progress}></div>
	</div>
}

type jobsUpdates struct{}

func (jobsUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	p := jobsPage{}
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		ids := f.IDs
		if f.Topic == resync || ids == nil && (f.Topic == "lab:job" || f.Topic == "lab:job-progress") {
			for _, j := range x.store.Jobs() {
				if err := x.patch(p.Card(j)); err != nil {
					return err
				}
			}
			return nil
		}
		for _, id := range ids {
			j, err := x.store.Job(id)
			if err != nil {
				continue
			}
			switch f.Topic {
			case "lab:job":
				err = x.patch(p.Card(j))
			case "lab:job-progress":
				err = x.patch(p.Progress(j))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

component jobsPanel() {
	<SimButton action="job-advance" id="j1">Job j1 progresses</SimButton>
	<SimButton action="job-step" id="j1">Job j1 changes status</SimButton>
}

// Case 8: URL state. The filter is in the URL (?status=) and in the signal
// $query.status. Without Datastar Pro (data-query-string, data-replace-url)
// the page keeps the two in step itself: a filter link sets the signal and
// pushes the URL, popstate sets the signal from the URL, and the layout
// restarts the stream whenever a query signal changes. The stream reads the
// filter from the signals and answers with the filtered list.

type catalogPage struct {
	updates catalogUpdates `route:"GET /updates Catalog updates"`
}

type catalogQuery struct {
	Status string `json:"status"`
}

var statuses = []string{"", "ok", "stale", "failing"}

component (p catalogPage) Page(r *http.Request, s *store, h *hub) {
	{{ q := catalogQuery{Status: r.URL.Query().Get("status")} }}
	<Layout title="Filtered catalog" stream={catalogUpdates{} |> url} query={q} s={s} h={h} panel={<catalogPanel/>}>
		<div class="contents" data-on:popstate__window="$query.status = new URLSearchParams(location.search).get('status') || ''">
			<nav class="flex gap-2 text-sm" data-testid="filters">
				{ for _, st := range statuses {
					<a
						{ if st == "" { href={catalogPage{} |> url} } else { href=f`@{catalogPage{} |> url}?status=@{st}` } }
						class="rounded border border-stone-300 px-2 py-1"
						data-class=js`{'bg-stone-800 text-white': $query.status === @{st}}`
						data-on:click__prevent=js`$query.status = @{st}; history.pushState(null, '', el.href)`
					>{ if st == "" { all } else { { st } } }</a>
				} }
			</nav>
			<p.List q={q} rows={s.Catalog(q.Status)}/>
		</div>
	</Layout>
}

component (p catalogPage) List(q catalogQuery, rows []series) {
	<section id={catalogPage.List |> id} class="flex flex-col gap-2">
		<p class="text-sm text-stone-500"><span data-testid="catalog-count">{ len(rows) }</span> series{ if q.Status != "" { {" "}with status { q.Status } } }</p>
		<table class="w-full text-sm" data-testid="catalog">
			<tbody>
				{ for _, s := range rows {
					<p.Row s={s}/>
				} }
			</tbody>
		</table>
	</section>
}

component (p catalogPage) Row(s series) {
	<tr id={catalogPage.Row |> key(s.ID)}>
		<td>{ s.Name }</td>
		<td data-testid="status">{ s.Status }</td>
	</tr>
}

type catalogUpdates struct{}

// A filtered list cannot be patched row by row without knowing which rows the
// tab shows, so any series change morphs the list; on (re)connect, which a
// filter change is, it sends the list for the signals' filter.
func (catalogUpdates) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub, v viewer, d *dropper) error {
	var in struct {
		Query catalogQuery `json:"query"`
	}
	if err := datastar.ReadSignals(r, &in); err != nil {
		return err
	}
	p := catalogPage{}
	return serveStream(w, r, s, h, v, d, func(x *push, f live.Frame) error {
		if f.Topic != resync && f.Topic != "lab:series" {
			return nil
		}
		return x.patch(p.List(in.Query, x.store.Catalog(in.Query.Status)))
	})
}

component catalogPanel() {
	<SimButton action="series-touch" id="s3">Change series s3</SimButton>
	<SimButton action="series-touch" id="s7">Change series s7</SimButton>
}
