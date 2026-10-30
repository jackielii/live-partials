// Counts what this tab spends on live updates, as the htmx lab's meter does:
// frames are events on the page stream; requests and their bytes exclude the
// stream and the simulate panel; bytes include every stream event; queries
// and server ms come from the stream's lab-meter events and the requests'
// Server-Timing and X-Queries; swaps are elements patched (one per signal
// patch or removal) and swapms the time Datastar spends applying them.
// Exposed on window.__meter for the e2e tests.
const zero = () => ({ frames: 0, requests: 0, bytes: 0, queries: 0, swaps: 0, server: 0, swapms: 0, urls: [] });
const m = { ...zero(), connects: 0 };
window.__meter = m;

const render = () => {
  for (const el of document.querySelectorAll("[data-meter]")) {
    const v = m[el.dataset.meter];
    el.textContent = typeof v === "number" ? Math.round(v * 10) / 10 : v;
  }
};
const utf8 = new TextEncoder();

// readStream counts the stream's events from a tee of its body.
async function readStream(body) {
  const reader = body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    let end;
    while ((end = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, end);
      buf = buf.slice(end + 2);
      const event = block.match(/^event: (.*)$/m)?.[1] ?? "";
      if (event === "lab-meter") {
        const [, server, queries] = block.match(/data: server ([\d.]+) queries (\d+)/) ?? [];
        m.server += parseFloat(server || 0);
        m.queries += Number(queries || 0);
      } else if (event.startsWith("datastar-")) {
        m.frames++;
        m.bytes += utf8.encode(block).length + 2;
      }
      render();
    }
  }
}

const realFetch = window.fetch;
window.fetch = async (input, init) => {
  const path = new URL(typeof input === "string" ? input : input.url, location.href).pathname;
  const res = await realFetch(input, init);
  if (path.endsWith("/updates")) {
    if (!res.body || !res.ok) return res;
    m.connects++;
    const [mine, theirs] = res.body.tee();
    readStream(mine).catch(() => {});
    return new Response(theirs, { status: res.status, statusText: res.statusText, headers: res.headers });
  }
  if (path.startsWith("/sim/")) return res;
  m.requests++;
  m.urls.push(path);
  m.bytes += utf8.encode(await res.clone().text()).length;
  m.server += parseFloat(res.headers.get("Server-Timing")?.match(/dur=([\d.]+)/)?.[1] ?? 0);
  m.queries += Number(res.headers.get("X-Queries") || 0);
  render();
  return res;
};

// patched counts the elements one patch event touches.
function patched({ type, argsRaw }) {
  if (type !== "datastar-patch-elements" || argsRaw.mode === "remove" || argsRaw.selector) return 1;
  const t = document.createElement("template");
  t.innerHTML = argsRaw.elements ?? "";
  return t.content.children.length;
}

// Datastar applies a patch in its own document listener for the
// (non-bubbling) datastar-fetch event: a window capture listener runs just
// before it, and a document listener added after Datastar loads (watchPatches,
// called from main.js) just after it.
let patchStart = null;
window.addEventListener(
  "datastar-fetch",
  (e) => {
    if (!e.detail.type.startsWith("datastar-patch")) return;
    m.swaps += patched(e.detail);
    patchStart = performance.now();
  },
  true,
);
export function watchPatches() {
  document.addEventListener("datastar-fetch", (e) => {
    if (patchStart == null || !e.detail.type.startsWith("datastar-patch")) return;
    m.swapms += performance.now() - patchStart;
    patchStart = null;
    render();
  });
}

document.addEventListener("click", (e) => {
  if (!e.target.closest("[data-meter-reset]")) return;
  Object.assign(m, zero());
  render();
});
