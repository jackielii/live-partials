// Filters used by the direct hx-trigger on each live element.

// hit reports whether a stream frame names key, or names nothing (every key).
window.hit = (detail, key) => {
  const ids = JSON.parse(detail.data || "{}").ids;
  return !ids || ids.includes(key);
};

const dirty = (el) =>
  [...el.querySelectorAll("input, textarea, select")].some((c) =>
    c.type === "checkbox" || c.type === "radio"
      ? c.checked !== c.defaultChecked
      : c.tagName === "SELECT"
        ? [...c.options].some((o) => o.selected !== o.defaultSelected)
        : c.value !== c.defaultValue,
  );

// cleanOrWarn lets a refetch through when el holds no unsaved input, and
// otherwise reveals its staleness notice instead.
window.cleanOrWarn = (el) => {
  if (!dirty(el)) return true;
  el.querySelector("[data-stale]")?.removeAttribute("hidden");
  return false;
};
