import { test } from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { plan, outermost, dirty, createDispatcher, setValues } from "./live.js";

function page(html) {
  const dom = new JSDOM(`<!DOCTYPE html><body>${html}</body>`, { url: "http://lab.test/series?q=1" });
  globalThis.location = dom.window.location;
  globalThis.CustomEvent = dom.window.CustomEvent;
  globalThis.Node = dom.window.Node;
  globalThis.performance ??= { now: () => Date.now() };
  return dom.window.document;
}

const ids = (els) => els.map((e) => e.id);

const rows = (n, attrs = "") =>
  Array.from({ length: n }, (_, i) => `<tr id="r${i + 1}" data-live="t" data-live-key="k${i + 1}" ${attrs}><td>${i + 1}</td></tr>`).join("");

test("a keyed element matches its key; an unkeyed listener matches any frame of its topic", () => {
  const doc = page(`
    <section id="tile" data-live="t"></section>
    <table><tbody id="list" data-live="t">${rows(10)}</tbody></table>
    <p id="other" data-live="u" data-live-key="k3"></p>`);
  assert.deepEqual(ids(plan(doc, "t", ["k3"])), ["tile", "r3"]);
});

test("a frame without ids matches the whole topic, outermost first", () => {
  const doc = page(`<section id="tile" data-live="t"></section><table><tbody id="list" data-live="t">${rows(4)}</tbody></table>`);
  assert.deepEqual(ids(plan(doc, "t", null)), ["tile", "list"]);
});

test("outer wins: a match inside another match is dropped", () => {
  const doc = page(`
    <article id="card" data-live="job" data-live-key="j1">
      <div id="bar" data-live="job" data-live-key="j1"></div>
      <p id="step" data-live="job" data-live-key="j1"></p>
    </article>
    <div id="bar2" data-live="job-progress" data-live-key="j1"></div>`);
  assert.deepEqual(ids(plan(doc, "job", ["j1"])), ["card"]);
  assert.deepEqual(ids(outermost([doc.getElementById("bar"), doc.getElementById("card")])), ["card"]);
});

test("the same key in many places matches every place", () => {
  const doc = page(`
    <li id="dm" data-live="presence" data-live-key="p1"></li>
    <div id="row" data-live="presence" data-live-key="p1"></div>
    <article id="card" data-live="presence" data-live-key="p1"></article>
    <article id="card2" data-live="presence" data-live-key="p2"></article>`);
  assert.deepEqual(ids(plan(doc, "presence", ["p1"])), ["dm", "row", "card"]);
});

test("escalation: past data-live-max matches the region is refetched instead", () => {
  const doc = page(`<table><tbody id="list" data-live="t" data-live-max="3">${rows(20)}</tbody></table>`);
  assert.deepEqual(ids(plan(doc, "t", ["k1", "k2", "k3"])), ["r1", "r2", "r3"]);
  assert.deepEqual(ids(plan(doc, "t", ["k1", "k2", "k3", "k4"])), ["list"]);
});

test("escalation: more than half of a region's keyed elements refetches the region", () => {
  const doc = page(`<table><tbody id="list" data-live="t">${rows(6)}</tbody></table>`);
  assert.deepEqual(ids(plan(doc, "t", ["k1", "k2", "k3"])), ["r1", "r2", "r3"]);
  assert.deepEqual(ids(plan(doc, "t", ["k1", "k2", "k3", "k4"])), ["list"]);
});

test("escalation defaults to 8", () => {
  const doc = page(`<table><tbody id="list" data-live="t">${rows(50)}</tbody></table>`);
  assert.equal(plan(doc, "t", Array.from({ length: 8 }, (_, i) => `k${i + 1}`)).length, 8);
  assert.deepEqual(ids(plan(doc, "t", Array.from({ length: 9 }, (_, i) => `k${i + 1}`))), ["list"]);
});

test("unknown ids go to the insert region; a region without data-live-insert ignores them", () => {
  const doc = page(`<ul id="files" data-live="f" data-live-insert>${'<li id="f1" data-live="f" data-live-key="f1"></li>'}</ul>
    <ul id="other" data-live="g"><li id="g1" data-live="g" data-live-key="g1"></li><li id="g2" data-live="g" data-live-key="g2"></li><li id="g3" data-live="g" data-live-key="g3"></li></ul>`);
  assert.deepEqual(ids(plan(doc, "f", ["f9"])), ["files"]);
  assert.deepEqual(ids(plan(doc, "g", ["g9"])), []);
});

test("dirty reads value against the rendered default", () => {
  const doc = page(`<form id="f"><input value="a"><input type="checkbox"><select><option>x</option><option selected>y</option></select><textarea>t</textarea></form>`);
  const f = doc.getElementById("f");
  assert.equal(dirty(f), false);
  f.querySelector("input").value = "b";
  assert.equal(dirty(f), true);
  f.querySelector("input").value = "a";
  f.querySelector("select").selectedIndex = 0;
  assert.equal(dirty(f), true);
});

function dispatcher(doc) {
  const calls = [];
  const swaps = [];
  let pendingTick = null;
  const d = createDispatcher({
    root: doc,
    htmx: {
      ajax: async (verb, url, opts) => calls.push({ verb, url, ids: opts.headers["X-Live-Targets"].split(" ") }),
      swap: (ctx) => swaps.push(ctx),
    },
    schedule: (fn) => (pendingTick = fn),
  });
  const tick = async () => {
    const fn = pendingTick;
    pendingTick = null;
    await fn?.();
  };
  return { d, calls, swaps, tick };
}

test("frames within one animation frame go out as one GET of the page, listing every target", async () => {
  const doc = page(`<section id="tile" data-live="t"></section><table><tbody id="list" data-live="t">${rows(10)}</tbody></table>`);
  const { d, calls, tick } = dispatcher(doc);
  d.frame("t", ["k3"]);
  d.frame("t", ["k5"]);
  d.frame("t", ["k3"]);
  await tick();
  assert.deepEqual(calls, [{ verb: "GET", url: "/series?q=1", ids: ["tile", "r3", "r5"] }]);
});

test("data-live-url sends that element in a GET of its own URL, in the same batch", async () => {
  const doc = page(`<li id="dm" data-live="p" data-live-key="1" data-live-url="/shell"></li><div id="row" data-live="p" data-live-key="1"></div>`);
  const { d, calls, tick } = dispatcher(doc);
  d.frame("p", ["1"]);
  await tick();
  assert.deepEqual(calls, [
    { verb: "GET", url: "/shell", ids: ["dm"] },
    { verb: "GET", url: "/series?q=1", ids: ["row"] },
  ]);
});

test("past maxTargets the rest wait for the next batch", async () => {
  const doc = page(`<table><tbody id="list" data-live="t" data-live-max="99">${rows(10)}</tbody></table>`);
  const calls = [];
  let tickFn;
  const d = createDispatcher({
    root: doc,
    maxTargets: 3,
    htmx: { ajax: async (v, u, o) => calls.push(o.headers["X-Live-Targets"]) },
    schedule: (fn) => (tickFn = fn),
  });
  d.frame("t", ["k1", "k2", "k3", "k4"]);
  await tickFn();
  await tickFn();
  assert.deepEqual(calls, ["r1 r2 r3", "r4"]);
});

test("one request in flight: frames arriving meanwhile wait for the next batch", async () => {
  const doc = page(`<table><tbody id="list" data-live="t">${rows(10)}</tbody></table>`);
  const calls = [];
  let release;
  let tickFn;
  const d = createDispatcher({
    root: doc,
    htmx: { ajax: (v, u, o) => (calls.push(o.headers["X-Live-Targets"].split(" ")), new Promise((r) => (release = r))) },
    schedule: (fn) => (tickFn = fn),
  });
  d.frame("t", ["k1"]);
  const first = tickFn();
  d.frame("t", ["k2"]);
  d.frame("t", ["k3"]);
  assert.equal(calls.length, 1);
  release();
  await first;
  const second = tickFn();
  release();
  await second;
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[1], ["r2", "r3"]);
});

test("a dirty element is not refetched; its notice is revealed", async () => {
  const doc = page(`
    <div id="d1" data-live="draft" data-live-key="d1"><form><input value="1"><p data-live-stale hidden>Changed</p></form></div>
    <div id="d2" data-live="draft" data-live-key="d2"><form><input value="2"><p data-live-stale hidden>Changed</p></form></div>`);
  doc.querySelector("#d1 input").value = "1x";
  const { d, calls, tick } = dispatcher(doc);
  let staleEvents = 0;
  doc.addEventListener("live:stale", () => staleEvents++);
  d.frame("draft", ["d1", "d2"]);
  await tick();
  assert.deepEqual(calls[0].ids, ["d2"]);
  assert.equal(doc.querySelector("#d1 [data-live-stale]").hidden, false);
  assert.equal(doc.querySelector("#d2 [data-live-stale]").hidden, true);
  assert.equal(staleEvents, 1);
});

test("an escalated region with a dirty row falls back to its clean rows", async () => {
  const doc = page(`<div id="grid" data-live="draft">${[1, 2, 3]
    .map((i) => `<div id="d${i}" data-live="draft" data-live-key="d${i}"><input value="${i}"><p data-live-stale hidden></p></div>`)
    .join("")}</div>`);
  doc.querySelector("#d1 input").value = "typed";
  const { d, calls, tick } = dispatcher(doc);
  d.frame("draft", ["d1", "d2", "d3"]);
  await tick();
  assert.deepEqual(calls[0].ids, ["d2", "d3"]);
  assert.equal(doc.querySelector("#d1 [data-live-stale]").hidden, false);
});

test("refresh discards unsaved input and refetches", async () => {
  const doc = page(`<div id="d1" data-live="draft" data-live-key="d1"><input value="1"></div>`);
  doc.querySelector("input").value = "typed";
  const { d, calls, tick } = dispatcher(doc);
  d.refresh(doc.getElementById("d1"));
  await tick();
  assert.equal(doc.querySelector("input").value, "1");
  assert.deepEqual(calls[0].ids, ["d1"]);
});

test("resync refetches every outermost live element of every topic", async () => {
  const doc = page(`<section id="tile" data-live="t"></section><table><tbody id="list" data-live="t">${rows(3)}</tbody></table>
    <article id="card" data-live="job" data-live-key="j1"><div id="bar" data-live="job-progress" data-live-key="j1"></div></article>`);
  const { d, calls, tick } = dispatcher(doc);
  d.resync();
  await tick();
  assert.deepEqual(calls[0].ids, ["tile", "list", "card"]);
});

test("Live-Skipped targets are queued again", async () => {
  const doc = page(`<table><tbody id="list" data-live="t">${rows(3)}</tbody></table>`);
  const { d, calls, tick } = dispatcher(doc);
  d.requeue(["r2", "gone"]);
  await tick();
  assert.deepEqual(calls[0].ids, ["r2"]);
});

test("value frames set every listener of the key, wherever it is and however many", () => {
  const dots = Array.from({ length: 50 }, (_, i) => `<span id="dot${i}" data-live-value="presence" data-live-value-key="p1" data-value="active"></span>`).join("");
  const doc = page(`${dots}
    <span id="text" data-live-value="presence" data-live-value-key="p1" data-live-value-into="text">active</span>
    <span id="p2" data-live-value="presence" data-live-value-key="p2" data-value="idle"></span>
    <span id="other" data-live-value="other" data-live-value-key="p1" data-value="x"></span>
    <span id="evil" data-live-value="presence" data-live-value-key="p1" data-live-value-into="href"></span>`);
  assert.equal(setValues(doc, "presence", { p1: "idle" }), 51);
  assert.equal(doc.querySelectorAll('[data-value="idle"]').length, 51); // fifty dots and p2
  assert.equal(doc.getElementById("text").textContent, "idle");
  assert.equal(doc.getElementById("p2").dataset.value, "idle");
  assert.equal(doc.getElementById("other").dataset.value, "x");
  assert.equal(doc.getElementById("evil").hasAttribute("href"), false);
});

test("a value frame's keys are also a signal for data-live elements of the topic", async () => {
  const doc = page(`<span id="dot" data-live-value="presence" data-live-value-key="p1" data-value="active"></span>
    <article id="card" data-live="presence" data-live-key="p1"></article>`);
  const { d, calls, tick } = dispatcher(doc);
  let seen;
  doc.addEventListener("live:values", (e) => (seen = e.detail));
  d.values("presence", { p1: "idle" });
  await tick();
  assert.equal(doc.getElementById("dot").dataset.value, "idle");
  assert.deepEqual(calls[0].ids, ["card"]);
  assert.equal(seen.elements, 1);
  assert.ok(d.listens("presence"));
});

test("a value frame for a page without data-live elements of the topic makes no request", async () => {
  const doc = page(`<span id="dot" data-live-value="presence" data-live-value-key="p1" data-value="active"></span>`);
  const { d, calls, tick } = dispatcher(doc);
  d.values("presence", { p1: "idle", p9: "active" });
  await tick();
  assert.equal(calls.length, 0);
  assert.ok(d.listens("presence"));
  assert.ok(!d.listens("series"));
});

const card = (id, inner = "") => `<article id="${id}" data-live="job" data-live-key="${id.slice(-2)}">${inner}</article>`;

test("a fragment is morphed into its element and its refetch is dropped", async () => {
  const doc = page(card("card--j1", `<p id="step--j1" data-live="job" data-live-key="j1"></p>`) + card("card--j2"));
  const { d, calls, swaps, tick } = dispatcher(doc);
  d.frame("job", ["j1"], [{ target: "card--j1", html: '<article id="card--j1">new</article>' }]);
  await tick();
  assert.equal(calls.length, 0);
  assert.equal(swaps.length, 1);
  assert.equal(swaps[0].swap, "none");
  assert.equal(swaps[0].text, '<hx-partial hx-target="#card--j1" hx-swap="outerMorph"><article id="card--j1">new</article></hx-partial>');
});

test("a fragment for an element the page does not show is ignored", async () => {
  const doc = page(card("card--j2"));
  const { d, calls, swaps, tick } = dispatcher(doc);
  d.frame("job", ["j1"], [{ target: "card--j1", html: "<article id='card--j1'></article>" }]);
  await tick();
  assert.equal(swaps.length, 0);
  assert.equal(calls.length, 0);
});

test("what no fragment covers is refetched: a failed render falls back to a pull", async () => {
  const doc = page(card("card--j1") + card("card--j2") + `<li id="dm--j1" data-live="job" data-live-key="j1"></li>`);
  const { d, calls, swaps, tick } = dispatcher(doc);
  d.frame("job", ["j1", "j2"], [{ target: "card--j1", html: '<article id="card--j1"></article>' }]);
  await tick();
  assert.equal(swaps.length, 1);
  assert.deepEqual(calls[0].ids, ["card--j2", "dm--j1"]);
});

test("outer wins: a fragment inside an element being refetched is dropped", async () => {
  const doc = page(`<table><tbody id="list" data-live="t">${rows(2)}</tbody></table>`);
  const { d, calls, swaps, tick } = dispatcher(doc);
  d.frame("t", ["k1", "k2"], [{ target: "r1", html: '<tr id="r1"></tr>' }]);
  await tick();
  assert.equal(swaps.length, 0);
  assert.deepEqual(calls[0].ids, ["list"]);
});

test("a fragment for a dirty editor is not morphed; its notice is revealed", async () => {
  const doc = page(`
    <form id="d1" data-live="draft" data-live-key="d1"><input value="1"><p data-live-stale hidden>Changed</p></form>
    <form id="d2" data-live="draft" data-live-key="d2"><input value="2"><p data-live-stale hidden>Changed</p></form>`);
  doc.querySelector("#d1 input").value = "1x";
  const { d, calls, swaps, tick } = dispatcher(doc);
  d.frame("draft", ["d1", "d2"], [
    { target: "d1", html: '<form id="d1"><input value="9"></form>' },
    { target: "d2", html: '<form id="d2"><input value="8"></form>' },
  ]);
  await tick();
  assert.equal(calls.length, 0);
  assert.equal(swaps.length, 1);
  assert.ok(!swaps[0].text.includes('id="d1"'));
  assert.ok(swaps[0].text.includes('id="d2"'));
  assert.equal(doc.querySelector("#d1 input").value, "1x");
  assert.equal(doc.querySelector("#d1 [data-live-stale]").hidden, false);
});
