// Counts what this tab spends on live updates: frames heard, requests made
// (excluding the stream and the simulate panel), response bytes, store
// queries, swaps, and server and swap time. Exposed on window.__meter for the e2e tests.
const m = { frames: 0, requests: 0, bytes: 0, queries: 0, swaps: 0, server: 0, swapms: 0, urls: [] };
window.__meter = m;

const counted = (ctx) => {
  const a = ctx?.request?.action || "";
  return !a.startsWith("/events") && !a.startsWith("/sim/");
};

const render = () => {
  for (const el of document.querySelectorAll("[data-meter]")) {
    const v = m[el.dataset.meter];
    el.textContent = typeof v === "number" ? Math.round(v * 10) / 10 : v;
  }
};

document.addEventListener("htmx:sse:after:message", (e) => {
  if (e.detail.message.event !== "resync") m.frames++;
  render();
});
document.addEventListener("htmx:before:request", (e) => {
  if (counted(e.detail.ctx)) e.detail.ctx.meterStart = performance.now();
});
document.addEventListener("htmx:after:request", (e) => {
  const ctx = e.detail.ctx;
  if (!counted(ctx)) return;
  m.requests++;
  m.urls.push(ctx.request.action);
  m.bytes += new TextEncoder().encode(ctx.text || "").length;
  const t = ctx.response.headers.get("Server-Timing")?.match(/dur=([\d.]+)/);
  m.server += t ? parseFloat(t[1]) : 0;
  m.queries += Number(ctx.response.headers.get("X-Queries") || 0);
  render();
});
document.addEventListener("htmx:before:swap", (e) => {
  if (!counted(e.detail.ctx)) return;
  e.detail.ctx.swapStart = performance.now();
  m.swaps += e.detail.tasks.filter((t) => t.target && t.swapSpec.style !== "none").length;
});
document.addEventListener("htmx:after:swap", (e) => {
  const ctx = e.detail.ctx;
  if (ctx.swapStart != null) m.swapms += performance.now() - ctx.swapStart;
  render();
});
document.addEventListener("click", (e) => {
  if (!e.target.closest("[data-meter-reset]")) return;
  Object.assign(m, { frames: 0, requests: 0, bytes: 0, queries: 0, swaps: 0, server: 0, swapms: 0, urls: [] });
  render();
});
