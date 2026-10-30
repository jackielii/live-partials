package main

import (
	"github.com/gsxhq/gsx"
	"github.com/gsxhq/vite"
)

component Layout(title string, panel gsx.Node, children gsx.Node, s *store, h *hub) {
	{{ assets := vite.FromContext(ctx).Entry("examples/lab/web/main.js") }}
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="UTF-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
			<title>{ title } · live partials lab</title>
			{ for _, href := range assets.CSS {
				<link rel="stylesheet" href={href}/>
			} }
			{ for _, src := range assets.JS {
				<script type="module" src={src}></script>
			} }
		</head>
		<body class="bg-stone-50 text-stone-900">
			<div id="stream" hx-sse:connect=f`/events?since=@{h.Since()}` hidden></div>
			<div class="grid min-h-screen grid-cols-[220px_1fr_300px]">
				<aside class="flex flex-col gap-4 border-r border-stone-200 bg-white p-4">
					<a href={homePage{} |> url} class="font-semibold">Live partials lab</a>
					<nav class="flex flex-col gap-1 text-sm">
						<a href={seriesPage{} |> url}>1 · Series catalog</a>
						<a href={peoplePage{} |> url}>2 · People</a>
						<a href={draftsPage{} |> url}>3 · Report draft</a>
						<a href={filesPage{} |> url}>4 · Files</a>
						<a href={burstPage{} |> url}>5 · Burst</a>
						<a href={jobsPage{} |> url}>6 · Jobs</a>
						<a href={catalogPage{} |> url}>8 · Filtered catalog</a>
					</nav>
					<h2 class="mt-4 text-xs font-semibold uppercase text-stone-500">Direct messages</h2>
					<ul class="flex flex-col gap-1 text-sm" data-testid="sidebar-people">
						{{ shell := shellPage{} }}
						{ for _, pr := range s.People() {
							<shell.DM pr={pr}/>
						} }
					</ul>
				</aside>
				<main class="flex flex-col gap-6 p-6">
					<h1 class="text-xl font-semibold">{ title }</h1>
					{ children }
				</main>
				<aside class="flex flex-col gap-4 border-l border-stone-200 bg-white p-4">
					<section id="meter" class="rounded border border-stone-200 p-3 text-xs" data-testid="meter">
						<h2 class="mb-2 font-semibold uppercase text-stone-500">This tab</h2>
						<dl class="grid grid-cols-2 gap-1">
							<dt>Frames</dt><dd data-meter="frames">0</dd>
							<dt>Requests</dt><dd data-meter="requests">0</dd>
							<dt>Bytes</dt><dd data-meter="bytes">0</dd>
							<dt>Queries</dt><dd data-meter="queries">0</dd>
							<dt>Swaps</dt><dd data-meter="swaps">0</dd>
							<dt>Server ms</dt><dd data-meter="server">0</dd>
							<dt>Swap ms</dt><dd data-meter="swapms">0</dd>
						</dl>
						<button type="button" class="mt-2 underline" data-meter-reset>Reset</button>
					</section>
					<section class="flex flex-col gap-2 rounded border border-amber-300 bg-amber-50 p-3 text-sm" data-testid="sim">
						<h2 class="text-xs font-semibold uppercase text-amber-800">Simulate other users</h2>
						{ panel }
						<SimButton action="drop-streams" id="">Restart the server's streams</SimButton>
					</section>
				</aside>
			</div>
		</body>
	</html>
}

component presenceDot(state string) {
	<span
		class={ "inline-block size-2.5 rounded-full", switch state { case "active": "bg-green-500" case "idle": "bg-amber-400" default: "bg-stone-300" } }
		data-presence={state}
		title={state}
	></span>
}

component SimButton(action string, id string, children gsx.Node) {
	<button
		type="button"
		class="rounded border border-amber-400 bg-white px-2 py-1 text-left hover:bg-amber-100"
		hx-post={simAction{} |> url(map[string]any{"action": action})}
		hx-vals=js`{"id": @{id}}`
		hx-swap="none"
	>{ children }</button>
}
