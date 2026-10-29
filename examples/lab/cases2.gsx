package main

import "net/http"

// Case 3: editor with unsaved input

type draftsPage struct{}

component (p draftsPage) Page(s *store) {
	<Layout title="Report draft" s={s} panel={<draftsPanel/>}>
		<p class="text-sm text-stone-600">Edit a value and leave it unsaved; changes by others to that row warn instead of overwriting.</p>
		<div class="flex flex-col gap-2" data-testid="drafts">
			{ for _, d := range s.Drafts() {
				<DraftRow d={d}/>
			} }
		</div>
	</Layout>
}

component DraftRow(d draftRow) {
	<div
		id={"draft-" + d.ID}
		data-key={d.ID}
		hx-get={draftRowPage{} |> url(map[string]any{"rowId": d.ID})}
		hx-trigger="lab:draft[hit(detail, this.dataset.key) && cleanOrWarn(this)] from:#stream"
		hx-swap="outerMorph"
	>
		<form
			class="flex items-center gap-2"
			hx-post={saveDraft{} |> url(map[string]any{"rowId": d.ID})}
			hx-target="closest [data-key]"
			hx-swap="outerMorph"
		>
			<label class="w-40 text-sm" for={"draft-input-" + d.ID}>{ d.Label }</label>
			<input id={"draft-input-" + d.ID} name="value" value={d.Value} class="rounded border border-stone-300 px-2 py-1"/>
			<button class="rounded border border-stone-300 px-2 py-1 text-sm">Save</button>
			{ if d.By != "" {
				<span class="text-xs text-stone-500">last saved by { d.By }</span>
			} }
			<p data-stale hidden class="text-xs text-amber-800">
				Changed by someone else ·
				<button type="button" class="underline" hx-get={draftRowPage{} |> url(map[string]any{"rowId": d.ID})} hx-target="closest [data-key]" hx-swap="outerHTML">Refresh</button>
			</p>
		</form>
	</div>
}

type draftRowPage struct{}

func (draftRowPage) Props(r *http.Request, s *store) (draftRow, error) {
	return s.Draft(r.PathValue("rowId"))
}

component (p draftRowPage) Page(d draftRow) {
	<DraftRow d={d}/>
}

type saveDraft struct{}

func (saveDraft) Props(r *http.Request, s *store, b *broadcaster) (draftRow, error) {
	id := r.PathValue("rowId")
	if err := s.SaveDraft(id, r.FormValue("value"), "you"); err != nil {
		return draftRow{}, err
	}
	b.publish("lab:draft", id)
	return s.Draft(id)
}

component (p saveDraft) Page(d draftRow) {
	<DraftRow d={d}/>
}

component draftsPanel() {
	<SimButton action="draft" id="d1">Someone edits Revenue</SimButton>
	<SimButton action="draft" id="d2">Someone edits Operating cost</SimButton>
}

// Case 4: insert and delete

type filesPage struct{}

component (p filesPage) Page(s *store) {
	<Layout title="Files" s={s} panel={<filesPanel/>}>
		<p.Listing files={s.Files()}/>
	</Layout>
}

component (p filesPage) Listing(files []file) {
	<section
		id={filesPage.Listing |> id}
		hx-get={filesPage{} |> url}
		hx-trigger="lab:files from:#stream, resync from:#stream"
		hx-swap="outerMorph"
	>
		<p class="text-sm text-stone-500"><span data-testid="file-count">{ len(files) }</span> files</p>
		<ul class="flex flex-col divide-y divide-stone-200 rounded border border-stone-200 bg-white" data-testid="files">
			{ for _, f := range files {
				<li id={"file-" + f.ID} class="flex justify-between px-3 py-1 text-sm"><span>{ f.Name }</span><span class="text-stone-500">{ f.Size / 1024 } KB</span></li>
			} }
		</ul>
	</section>
}

func (p filesPage) Props(s *store) []file {
	return s.Files()
}

component filesPanel() {
	<SimButton action="file-add" id="">Someone uploads a file</SimButton>
	<SimButton action="file-delete" id="f2">Someone deletes minutes.docx</SimButton>
}

// Case 5: burst

type burstPage struct{}

component (p burstPage) Page(s *store) {
	<Layout title="Burst" s={s} panel={<SimButton action="burst" id="">50 rows change within 100 ms</SimButton>}>
		<table class="w-full text-sm" data-testid="burst">
			<tbody
				id={burstPage.Rows |> id}
				hx-get={burstPage{} |> url}
				hx-trigger="resync from:#stream"
				hx-swap="innerMorph"
			>
				<p.Rows rows={s.Burst()}/>
			</tbody>
		</table>
	</Layout>
}

component (p burstPage) Rows(rows []burstRow) {
	{ for _, b := range rows {
		<BurstRow b={b}/>
	} }
}

func (p burstPage) Props(s *store) []burstRow {
	return s.Burst()
}

component BurstRow(b burstRow) {
	<tr
		id={"burst-" + b.ID}
		data-key={b.ID}
		hx-get={burstRowPage{} |> url(map[string]any{"rowId": b.ID})}
		hx-trigger="lab:burst[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
	>
		<td>{ b.ID }</td>
		<td data-testid="value">{ b.Value }</td>
	</tr>
}

type burstRowPage struct{}

func (burstRowPage) Props(r *http.Request, s *store) (burstRow, error) {
	return s.BurstRow(r.PathValue("rowId"))
}

component (p burstRowPage) Page(b burstRow) {
	<BurstRow b={b}/>
}

// Case 6: nested levels

type jobsPage struct{}

component (p jobsPage) Page(s *store) {
	<Layout title="Jobs" s={s} panel={<jobsPanel/>}>
		<div class="flex flex-col gap-3">
			{ for _, j := range s.Jobs() {
				<JobCard j={j}/>
			} }
		</div>
	</Layout>
}

component JobCard(j job) {
	<article
		id={"job-" + j.ID}
		data-key={j.ID}
		data-testid={"job-" + j.ID}
		class="rounded border border-stone-200 bg-white p-3"
		hx-get={jobPart{} |> url(map[string]any{"jobId": j.ID, "view": "card"})}
		hx-trigger="lab:job[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
	>
		<h3 class="font-semibold">{ j.Name } <span class="text-xs text-stone-500" data-testid="job-status">{ j.Status }</span></h3>
		<JobProgress j={j}/>
		<JobStep j={j}/>
	</article>
}

component JobProgress(j job) {
	<div
		id={"job-progress-" + j.ID}
		data-key={j.ID}
		class="my-2 h-2 rounded bg-stone-200"
		hx-get={jobPart{} |> url(map[string]any{"jobId": j.ID, "view": "progress"})}
		hx-trigger="lab:job-progress[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
	>
		<div class="h-2 rounded bg-sky-500" style=css`width: @{j.Progress}%` data-testid="job-progress" data-progress={j.Progress}></div>
	</div>
}

component JobStep(j job) {
	<p
		id={"job-step-" + j.ID}
		data-key={j.ID}
		class="text-xs text-stone-500"
		hx-get={jobPart{} |> url(map[string]any{"jobId": j.ID, "view": "step"})}
		hx-trigger="lab:job[hit(detail, this.dataset.key)] from:#stream"
		hx-swap="outerMorph"
		data-testid="job-step"
	>{ j.Step }</p>
}

type jobPart struct{}

func (jobPart) Props(r *http.Request, s *store) (job, error) {
	return s.Job(r.PathValue("jobId"))
}

component (p jobPart) Page(j job, r *http.Request) {
	{ switch r.PathValue("view") {
	case "progress":
		<JobProgress j={j}/>
	case "step":
		<JobStep j={j}/>
	default:
		<JobCard j={j}/>
	} }
}

component jobsPanel() {
	<SimButton action="job-advance" id="j1">Job j1 progresses</SimButton>
	<SimButton action="job-step" id="j1">Job j1 changes status</SimButton>
}
