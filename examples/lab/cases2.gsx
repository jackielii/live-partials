package main

import (
	"net/http"

	"github.com/jackielii/structpages"

	"github.com/jackielii/live-partials/live"
)

// Case 3: editor with unsaved input

type draftsPage struct{}

func (p draftsPage) Props(r *http.Request, s *store, t structpages.RenderTarget) (draftRow, error) {
	if t.Is(p.Row) {
		return s.Draft(structpages.KeyOf(t))
	}
	return draftRow{}, nil
}

component (p draftsPage) Page(s *store, h *hub) {
	<Layout title="Report draft" s={s} h={h} panel={<draftsPanel/>}>
		<p class="text-sm text-stone-600">Edit a value and leave it unsaved; changes by others to that row warn instead of overwriting.</p>
		<div class="flex flex-col gap-2" data-testid="drafts">
			{ for _, d := range s.Drafts() {
				<p.Row d={d}/>
			} }
		</div>
	</Layout>
}

component (p draftsPage) Row(d draftRow) {
	<form
		id={draftsPage.Row |> key(d.ID)}
		{ live.On("lab:draft", d.ID)... }
		class="flex items-center gap-2"
		hx-post={saveDraft{} |> url(map[string]any{"rowId": d.ID})}
		hx-target="this"
		hx-swap="outerMorph"
	>
		<label class="w-40 text-sm" for={"draft-input-" + d.ID}>{ d.Label }</label>
		<input id={"draft-input-" + d.ID} name="value" value={d.Value} class="rounded border border-stone-300 px-2 py-1"/>
		<button class="rounded border border-stone-300 px-2 py-1 text-sm">Save</button>
		{ if d.By != "" {
			<span class="text-xs text-stone-500">last saved by { d.By }</span>
		} }
		<p data-live-stale hidden class="text-xs text-amber-800">
			Changed by someone else ·{" "}
			<button type="button" class="underline" data-live-refresh>Refresh</button>
		</p>
	</form>
}

type saveDraft struct{}

func (saveDraft) ServeHTTP(w http.ResponseWriter, r *http.Request, s *store, h *hub) error {
	id := r.PathValue("rowId")
	if err := s.SaveDraft(id, r.FormValue("value"), "you"); err != nil {
		return err
	}
	h.Publish(r.Context(), everyone, "lab:draft", id)
	d, err := s.Draft(id)
	if err != nil {
		return err
	}
	return structpages.RenderComponent(draftsPage{}.Row(d))
}

component draftsPanel() {
	<SimButton action="draft" id="d1">Someone edits Revenue</SimButton>
	<SimButton action="draft" id="d2">Someone edits Operating cost</SimButton>
}

// Case 4: insert and delete

type filesPage struct{}

type fileCount int

func (p filesPage) Props(r *http.Request, s *store, t structpages.RenderTarget) ([]file, file, fileCount, error) {
	if t.Is(p.Item) {
		f, err := s.File(structpages.KeyOf(t))
		return nil, f, 0, err
	}
	files := s.Files()
	return files, file{}, fileCount(len(files)), nil
}

component (p filesPage) Page(files []file, n fileCount, s *store, h *hub) {
	<Layout title="Files" s={s} h={h} panel={<filesPanel/>}>
		<p.Count n={n}/>
		<p.List files={files}/>
	</Layout>
}

component (p filesPage) Count(n fileCount) {
	<p id={filesPage.Count |> id} { live.On("lab:files")... } class="text-sm text-stone-500">
		<span data-testid="file-count">{ int(n) }</span> files
	</p>
}

component (p filesPage) List(files []file) {
	<ul
		id={filesPage.List |> id}
		{ live.On("lab:files")... }
		data-live-insert
		class="flex flex-col divide-y divide-stone-200 rounded border border-stone-200 bg-white"
		data-testid="files"
	>
		{ for _, f := range files {
			<p.Item f={f}/>
		} }
	</ul>
}

component (p filesPage) Item(f file) {
	<li id={filesPage.Item |> key(f.ID)} { live.On("lab:files", f.ID)... } class="flex justify-between px-3 py-1 text-sm">
		<span>{ f.Name }</span><span class="text-stone-500">{ f.Size / 1024 } KB</span>
	</li>
}

component filesPanel() {
	<SimButton action="file-add" id="">Someone uploads a file</SimButton>
	<SimButton action="file-delete" id="f2">Someone deletes minutes.docx</SimButton>
}

// Case 5: burst

type burstPage struct{}

func (p burstPage) Props(r *http.Request, s *store, t structpages.RenderTarget) ([]burstRow, burstRow, error) {
	if t.Is(p.Row) {
		b, err := s.BurstRow(structpages.KeyOf(t))
		return nil, b, err
	}
	return s.Burst(), burstRow{}, nil
}

component (p burstPage) Page(rows []burstRow, s *store, h *hub) {
	<Layout title="Burst" s={s} h={h} panel={<SimButton action="burst" id="">50 rows change within 100 ms</SimButton>}>
		<table class="w-full text-sm" data-testid="burst">
			<p.Rows rows={rows}/>
		</table>
	</Layout>
}

component (p burstPage) Rows(rows []burstRow) {
	<tbody id={burstPage.Rows |> id} { live.On("lab:burst")... }>
		{ for _, b := range rows {
			<p.Row b={b}/>
		} }
	</tbody>
}

component (p burstPage) Row(b burstRow) {
	<tr id={burstPage.Row |> key(b.ID)} { live.On("lab:burst", b.ID)... }>
		<td>{ b.ID }</td>
		<td data-testid="value">{ b.Value }</td>
	</tr>
}

// Case 6: nested levels

type jobsPage struct{}

func (p jobsPage) Props(r *http.Request, s *store, t structpages.RenderTarget) (job, error) {
	if t.Is(p.Card) || t.Is(p.Progress) || t.Is(p.Step) {
		return s.Job(structpages.KeyOf(t))
	}
	return job{}, nil
}

component (p jobsPage) Page(s *store, h *hub) {
	<Layout title="Jobs" s={s} h={h} panel={<jobsPanel/>}>
		<div class="flex flex-col gap-3">
			{ for _, j := range s.Jobs() {
				<p.Card j={j}/>
			} }
		</div>
	</Layout>
}

component (p jobsPage) Card(j job) {
	<article
		id={jobsPage.Card |> key(j.ID)}
		{ live.On("lab:job", j.ID)... }
		data-testid={"job-" + j.ID}
		class="rounded border border-stone-200 bg-white p-3"
	>
		<h3 class="font-semibold">{ j.Name } <span class="text-xs text-stone-500" data-testid="job-status">{ j.Status }</span></h3>
		<p.Progress j={j}/>
		<p.Step j={j}/>
	</article>
}

component (p jobsPage) Progress(j job) {
	<div id={jobsPage.Progress |> key(j.ID)} { live.On("lab:job-progress", j.ID)... } class="my-2 h-2 rounded bg-stone-200">
		<div class="h-2 rounded bg-sky-500" style=css`width: @{j.Progress}%` data-testid="job-progress" data-progress={j.Progress}></div>
	</div>
}

component (p jobsPage) Step(j job) {
	<p id={jobsPage.Step |> key(j.ID)} { live.On("lab:job", j.ID)... } class="text-xs text-stone-500" data-testid="job-step">{ j.Step }</p>
}

component jobsPanel() {
	<SimButton action="job-advance" id="j1">Job j1 progresses</SimButton>
	<SimButton action="job-step" id="j1">Job j1 changes status</SimButton>
}
