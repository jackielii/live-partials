# structpages: keyed component targets — problem and proposal

## Problem

A page component is often rendered once per item: a table row, a card, a person's dot. To re-render
**one instance** today, the app needs three things structpages doesn't provide:

1. **An id per instance.** `|> id` gives one id per component, so every row shares it. Apps
   hand-build `@{Page.Row |> id}-@{key}`. EOS has many of these.
2. **A request that selects that instance's component.** `HTMXv4RenderTarget` can't match
   `HX-Target: tr#list-row-<key>`: pass 0 finds no exact id, and no suffix rule matches a key that
   follows the component id. The request falls back to `Page`, so a whole document is swapped into
   the row (repro: `live/batch_test.go: TestStockSelectorMissesKeyedTargets`).
3. **A way for Props to learn which instance.** Without it, each keyed component gets its own route
   and page struct just to read a path parameter. In the live-partials lab that was 5 of 14 routes.

Live updates make this common: a pushed event names the ids that changed, and the page should
re-render exactly those instances. Ordinary htmx needs it too, for example a row's own
"Reload" or "Edit" button targeting that row.

## Constraints

- **The key must ride in the element id.** htmx sends `HX-Target` from the target element
  automatically, so any `hx-target` pointing at the instance works with no extra markup.
- **Ids must stay unique and parseable.** The component part keeps the exact-match guarantee
  (pass 0). The key must be recoverable, so it can't be hashed into the `maxIDLen` compaction.
  Keys are arbitrary strings such as UUIDs, so they need escaping into id-safe characters.
- **The key is client input.** Like a path parameter, Props must authorise it, so the docs say so.
- **Existing custom `TargetSelector`s keep compiling.**

## Proposal

1. **Generate.** `ID(ctx, v, key...)` and `IDTarget(ctx, v, key...)`, and in gsx `|> id(key)` and
   `|> target(key)`.
   - The output is the component id, a separator generated ids never contain, and the escaped key.
   - The key is exempt from the length budget, which applies to the component part only.
2. **Select.** The built-in htmx selectors split the key off `HX-Target` before matching, so
   component matching is unchanged.
3. **Read.** Props receives the key **by dependency injection**, as it receives other arguments: an
   injectable `structpages.TargetKey` (a string type), empty for an unkeyed target. Example:
   `func (p Page) Props(r *http.Request, t structpages.RenderTarget, key structpages.TargetKey)`.
   This doesn't change the `RenderTarget` interface, so custom selectors aren't broken.
4. **Lint.** `structpages-lint` checks `id(key)` and `target(key)` as it checks `id` and `target`.

**Usage after:**

```gsx
<tr id={ Page.Row |> id(row.ID) }>…<button hx-get={ Page{} |> url } hx-target={ Page.Row |> target(row.ID) }>Reload</button></tr>
```

```go
case t.Is(p.Row):
	row, err := store.Row(ctx, string(key))
```

## Open questions for structpages

- **The separator and escaping alphabet.** The lab uses `--` and `[A-Za-z0-9_-]`, with any other
  byte written as `_XX`.
- **Several keys per instance**, such as a cell identified by row and column. They could be a single
  composite key, joined by the app.
- **Who answers 404 for an unknown key.** Props can return a not-found error as today, or structpages
  can provide a helper for it.
