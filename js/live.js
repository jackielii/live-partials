// live.js: applies stream frames to the elements that show what changed. An
// element is live when it carries data-live="<topic>" and, for one keyed
// instance, data-live-key="<key>" (live.On on the server). A frame is a
// signal ({"ids":[…]}), a value frame ({"values":{key:value}}) or a signal
// carrying rendered fragments ({"ids":[…],"fragments":[{target,html}]}).
//
// Value frames are written into every value listener of the topic and key
// (data-live-value="<topic>" data-live-value-key="<key>", live.Value): into
// its data-value attribute, or its text with data-live-value-into="text".
// Nothing is fetched. Their keys are also a signal for data-live elements.
//
// Fragments are morphed into the element with the fragment's id when the page
// has it (and ignored when not); a planned refetch the fragments cover is
// dropped. Whatever no fragment covers is handled as a signal: for each frame
// it (1) matches elements, (2) keeps only the outermost,
// (3) escalates to the enclosing region past data-live-max (8) or half of
// the region's keyed elements, (4) sends ids that match nothing to regions
// marked data-live-insert, (5) batches every refetch of an animation frame
// into one GET per page URL, listing the element ids in X-Live-Targets,
// (6) leaves elements with unsaved input alone and reveals their
// [data-live-stale] notice, and (7) on resync refetches every outermost live
// element and applies the current values the resync carries. The page's live
// middleware answers with <hx-partial> fragments that htmx swaps; a 404
// arrives as a delete.

const quote = (s) => `"${s.replace(/["\\]/g, "\\$&")}"`;
const topicSelector = (topic) => `[data-live~=${quote(topic)}]`;
const valueSelector = (topic) => `[data-live-value~=${quote(topic)}]`;
const isKeyed = (el) => el.hasAttribute("data-live-key");

// outermost drops every element that sits inside another one of els and
// returns the rest in document order.
export function outermost(els) {
  const set = [...new Set(els)];
  return set
    .filter((el) => !set.some((o) => o !== el && o.contains(el)))
    .sort((a, b) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1));
}

// dirty reports whether el holds a form control whose value differs from
// the one it was rendered with.
export function dirty(el) {
  for (const c of el.querySelectorAll("input, textarea, select")) {
    if (c.type === "checkbox" || c.type === "radio") {
      if (c.checked !== c.defaultChecked) return true;
    } else if (c.tagName === "SELECT") {
      if ([...c.options].some((o) => o.selected !== o.defaultSelected)) return true;
    } else if (c.type !== "hidden" && c.type !== "file" && c.value !== c.defaultValue) {
      return true;
    }
  }
  return false;
}

// topicIndex finds a topic's live elements and its regions: unkeyed live
// elements of the topic that contain keyed ones. A region is refetched only
// for an id-less frame, an escalation, or an unknown id (data-live-insert);
// otherwise every keyed change would re-render it and precision would be lost.
function topicIndex(root, topic) {
  const all = [...root.querySelectorAll(topicSelector(topic))];
  const keyed = all.filter(isKeyed);
  const regions = all.filter((el) => !isKeyed(el) && keyed.some((k) => el.contains(k)));
  const regionOf = (el) => {
    for (let p = el.parentElement; p; p = p.parentElement) if (regions.includes(p)) return p;
    return null;
  };
  return { all, keyed, regions, regionOf };
}

// plan returns the elements to refetch for one frame. ids null means the
// whole topic.
export function plan(root, topic, ids) {
  const { all, keyed, regions, regionOf } = topicIndex(root, topic);
  if (!ids) return outermost(all);

  const want = new Set(ids);
  const hits = keyed.filter((el) => want.has(el.dataset.liveKey));
  hits.push(...all.filter((el) => !isKeyed(el) && !regions.includes(el)));

  const known = new Set(hits.filter(isKeyed).map((el) => el.dataset.liveKey));
  if (ids.some((id) => !known.has(id))) {
    hits.push(...regions.filter((r) => r.hasAttribute("data-live-insert")));
  }
  for (const r of regions) {
    const mine = keyed.filter((k) => regionOf(k) === r);
    const hit = mine.filter((k) => want.has(k.dataset.liveKey)).length;
    if (hit > Number(r.dataset.liveMax || 8) || hit * 2 > mine.length) hits.push(r);
  }
  return outermost(hits);
}

// spare replaces a dirty element by what can still be refetched safely: a
// dirty region yields its clean keyed elements that matched; a dirty keyed
// element is reported stale.
function spare(els, root, topic, ids) {
  const fetch = [];
  const stale = [];
  const visit = (el) => {
    if (!dirty(el)) return fetch.push(el);
    const inner = [...el.querySelectorAll(topic ? topicSelector(topic) : "[data-live]")].filter(
      (k) => isKeyed(k) && (!ids || ids.includes(k.dataset.liveKey)),
    );
    if (isKeyed(el) || inner.length === 0) return stale.push(el);
    outermost(inner).forEach(visit);
  };
  els.forEach(visit);
  return { fetch, stale };
}

// setValues writes values (key → value) into every value listener of topic
// whose key has one, and returns how many listeners it set. A listener
// writes into its text (data-live-value-into="text") or into a data- or
// aria- attribute it names (data-value by default); other names are refused,
// so a value can never become a URL or a handler.
export function setValues(root, topic, values) {
  let n = 0;
  for (const el of root.querySelectorAll(valueSelector(topic))) {
    const key = el.dataset.liveValueKey ?? "";
    if (!Object.hasOwn(values, key)) continue;
    const v = String(values[key]);
    const into = el.dataset.liveValueInto || "data-value";
    if (into === "text") {
      if (el.textContent !== v) el.textContent = v;
    } else if (/^(data|aria)-/.test(into)) {
      if (el.getAttribute(into) !== v) el.setAttribute(into, v);
    } else {
      console.warn("live: data-live-value-into must be text, data-* or aria-*", el);
      continue;
    }
    n++;
  }
  return n;
}

const cssID = (id) => (globalThis.CSS?.escape ? CSS.escape(id) : id.replace(/[^\w-]/g, "\\$&"));
const attr = (s) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;");

export const TARGETS_HEADER = "X-Live-Targets";

// createDispatcher holds the batching state. It is separate from start so
// tests can drive it without a stream.
export function createDispatcher({ root = document, htmx, maxTargets = 64, schedule } = {}) {
  const pending = new Map();
  let scheduled = false;
  let inflight = false;
  const tick =
    schedule ??
    ((fn) => {
      let done = false;
      const once = () => !done && ((done = true), fn());
      requestAnimationFrame(once);
      setTimeout(once, 50);
    });

  function queue(el) {
    if (!el.id) {
      console.warn("live: a live element needs an id to be refetched", el);
      return;
    }
    pending.set(el.id, el.dataset.liveUrl || location.pathname + location.search);
    kick();
  }

  function kick() {
    if (scheduled || inflight || pending.size === 0) return;
    scheduled = true;
    tick(flush);
  }

  // flush sends one GET per page URL, each naming at most maxTargets ids;
  // the rest wait for the next batch.
  async function flush() {
    scheduled = false;
    if (inflight || pending.size === 0) return;
    const byURL = new Map();
    for (const [id, url] of pending) {
      const ids = byURL.get(url) ?? byURL.set(url, []).get(url);
      if (ids.length < maxTargets) ids.push(id);
    }
    for (const ids of byURL.values()) ids.forEach((id) => pending.delete(id));
    inflight = true;
    try {
      await Promise.all(
        [...byURL].map(([url, ids]) =>
          htmx.ajax("GET", url, {
            source: root.body ?? root,
            target: root.body ?? root,
            swap: "none",
            headers: { [TARGETS_HEADER]: ids.join(" ") },
          }),
        ),
      );
    } finally {
      inflight = false;
      kick();
    }
  }

  function markStale(el) {
    const slot = el.querySelector("[data-live-stale]") ?? el.closest("form")?.querySelector("[data-live-stale]");
    slot?.removeAttribute("hidden");
    el.dispatchEvent(new CustomEvent("live:stale", { bubbles: true }));
  }

  function apply(els, topic, ids) {
    const { fetch, stale } = spare(els, root, topic, ids);
    stale.forEach(markStale);
    fetch.forEach(queue);
  }

  // push morphs each fragment into the element with its id, in one htmx swap
  // so the page's swap events and processing apply. Fragments whose element
  // is absent are ignored; one inside a planned refetch is left to it (outer
  // wins); a dirty element is spared as for a refetch. It returns the planned
  // refetches no fragment covers.
  function push(planned, fragments, topic, ids) {
    const html = new Map();
    for (const f of fragments) {
      const el = root.getElementById?.(f.target) ?? root.querySelector?.(`#${cssID(f.target)}`);
      if (el) html.set(el, f.html);
    }
    if (html.size === 0) return planned;
    let targets = outermost([...html.keys()]);
    const pulls = planned.filter((el) => !targets.some((t) => t === el || t.contains(el)));
    targets = targets.filter((t) => !pulls.some((p) => p.contains(t)));
    const clean = targets.filter((t) => !dirty(t));
    const { fetch, stale } = spare(targets.filter(dirty), root, topic, ids);
    stale.forEach(markStale);
    if (clean.length) {
      const text = clean
        .map((t) => `<hx-partial hx-target="${attr("#" + cssID(t.id))}" hx-swap="outerMorph">${html.get(t)}</hx-partial>`)
        .join("");
      htmx.swap({ text, sourceElement: root.body ?? root, swap: "none" });
    }
    return [...pulls, ...fetch];
  }

  return {
    // frame handles one topic frame; ids null means the whole topic.
    // fragments are elements the stream rendered for this frame.
    frame(topic, ids, fragments = []) {
      apply(push(plan(root, topic, ids), fragments, topic, ids), topic, ids);
    },
    // values applies a value frame and signals its keys to data-live
    // elements of the topic.
    values(topic, values) {
      const start = performance.now();
      const n = setValues(root, topic, values);
      (root.body ?? root).dispatchEvent?.(
        new CustomEvent("live:values", { bubbles: true, detail: { topic, elements: n, ms: performance.now() - start } }),
      );
      const keys = Object.keys(values);
      if (keys.length && root.querySelector(topicSelector(topic))) this.frame(topic, keys);
    },
    resync() {
      apply(outermost([...root.querySelectorAll("[data-live]")]), null, null);
    },
    // refresh discards el's unsaved input and refetches it.
    refresh(el) {
      for (const c of el.querySelectorAll("input, textarea, select")) {
        if (c.type === "checkbox" || c.type === "radio") c.checked = c.defaultChecked;
        else if (c.tagName === "SELECT") for (const o of c.options) o.selected = o.defaultSelected;
        else if (c.type !== "file") c.value = c.defaultValue;
      }
      queue(el);
    },
    requeue(ids) {
      for (const id of ids) {
        const el = root.getElementById?.(id);
        if (el) queue(el);
      }
    },
    listens(topic) {
      return !!root.querySelector(`${topicSelector(topic)}, ${valueSelector(topic)}`);
    },
    flush,
    pending,
  };
}

// start wires the dispatcher to the page: frames from the SSE element
// matching stream, refresh buttons ([data-live-refresh]) and the Live-Skipped
// header of its own requests. It returns a function that unwires it.
export function start({ stream = "#stream", htmx = window.htmx } = {}) {
  const d = createDispatcher({ htmx });
  const onMessage = (e) => {
    if (!e.target.matches?.(stream)) return;
    const { event, data } = e.detail.message;
    if (!event) return;
    let msg = {};
    try {
      msg = JSON.parse(data || "{}") ?? {};
    } catch {}
    if (event === "resync") {
      for (const [topic, values] of Object.entries(msg.values ?? {})) setValues(document, topic, values);
      return d.resync();
    }
    // Keep frames something else on the page triggers on (hx-trigger="topic from:…").
    if (!d.listens(event) && !document.querySelector(`[hx-trigger*=${quote(event)}]`)) {
      e.detail.cancelled = true;
      return;
    }
    if (msg.values) d.values(event, msg.values);
    else d.frame(event, msg.ids ?? null, msg.fragments ?? []);
  };
  const onClick = (e) => {
    const btn = e.target.closest?.("[data-live-refresh]");
    const el = btn?.closest("[data-live]");
    if (!el) return;
    e.preventDefault();
    d.refresh(el);
  };
  const onResponse = (e) => {
    const ctx = e.detail.ctx;
    if (!ctx.request?.headers?.[TARGETS_HEADER]) return;
    const skipped = ctx.response?.headers?.get("Live-Skipped");
    if (skipped) d.requeue(skipped.split(" "));
  };
  document.addEventListener("htmx:sse:before:message", onMessage);
  document.addEventListener("click", onClick);
  document.addEventListener("htmx:after:request", onResponse);
  return () => {
    document.removeEventListener("htmx:sse:before:message", onMessage);
    document.removeEventListener("click", onClick);
    document.removeEventListener("htmx:after:request", onResponse);
  };
}
