import { test, expect, type Page } from "@playwright/test";
import { open, meter, sim, post, settle } from "./helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/sim/reset");
});

// watch records which of the elements matching selector (by id) have their
// subtree changed from now on.
async function watch(page: Page, selector: string) {
  await page.evaluate((sel) => {
    const w = window as any;
    w.__changed = new Set<string>();
    w.__obs?.disconnect();
    w.__obs = new MutationObserver((records) => {
      for (const r of records) {
        const el = (r.target instanceof Element ? r.target : r.target.parentElement)?.closest(sel);
        if (el) w.__changed.add(el.id);
      }
    });
    w.__obs.observe(document.body, { subtree: true, childList: true, characterData: true, attributes: true });
  }, selector);
}
const changed = (page: Page): Promise<string[]> => page.evaluate(() => [...(window as any).__changed].sort());

test("1 keyed list + aggregate: one row change updates that row and the tile only, in two tabs", async ({ page, context }) => {
  const other = await context.newPage();
  await open(page, "/series");
  await open(other, "/series");
  for (const p of [page, other]) await watch(p, "#series-summary, tr[id]");
  await sim(page, "Change series s3");
  for (const p of [page, other]) {
    await expect(p.locator("#series-row--s3 [data-testid=points]")).toHaveText("307");
    await expect(p.getByTestId("sum-points")).toHaveText("5507");
    await settle(p);
    expect(await changed(p)).toEqual(["series-row--s3", "series-summary"]);
    const m = await meter(p);
    // One patch-elements event per element: a <tr> cannot share one with a <section>.
    expect([m.requests, m.frames, m.swaps]).toEqual([0, 2, 2]);
  }
});

test("1 three rows in one frame are four events and one row query", async ({ page }) => {
  await open(page, "/series");
  await sim(page, "Change series s2, s5 and s8 together");
  await expect(page.locator("#series-row--s8 [data-testid=points]")).toHaveText("807");
  await settle(page);
  const m = await meter(page);
  expect([m.frames, m.swaps, m.queries]).toEqual([4, 4, 2]);
});

test("2 same key in many places: a presence change reaches sidebar, directory and card with no request", async ({ page }) => {
  await open(page, "/people");
  await watch(page, "li[id], tr[id], article[id]");
  await sim(page, "Ada changes presence");
  for (const sel of ["#main-dm--p1", "#people-row--p1", "#people-card--p1"]) {
    await expect(page.locator(`${sel} [data-presence]`).first()).toHaveAttribute("data-presence", "idle");
  }
  await settle(page);
  expect((await changed(page)).filter((id) => !id.endsWith("p1"))).toEqual([]);
  const m = await meter(page);
  expect([m.frames, m.requests, m.swaps]).toEqual([3, 0, 3]);
});

test("2 a person without a card patches only the sidebar and the row", async ({ page }) => {
  await open(page, "/people");
  await sim(page, "Edsger changes presence");
  await expect(page.locator("#people-row--p4 [data-presence]")).toHaveAttribute("data-presence", "idle");
  await settle(page);
  expect((await meter(page)).swaps).toBe(2);
});

test("3 editor: a dirty row warns instead of changing, a neighbour changes without moving the caret", async ({ page }) => {
  await open(page, "/drafts");
  const d1 = page.locator("#draft-input-d1");
  await d1.click();
  await d1.press("End");
  await d1.pressSequentially("5");
  await d1.press("ArrowLeft");
  await d1.press("ArrowLeft");
  const caret = await d1.evaluate((el: HTMLInputElement) => el.selectionStart);

  await sim(page, "Someone edits Operating cost");
  await expect(page.locator("#draft-input-d2")).toHaveValue("4007");
  await expect(page.locator("#drafts-row--d2")).toContainText("last saved by Grace");
  await expect(d1).toBeFocused();
  expect(await d1.evaluate((el: HTMLInputElement) => el.selectionStart)).toBe(caret);
  await expect(d1).toHaveValue("10005");

  await sim(page, "Someone edits Revenue");
  await expect(page.locator("#drafts-row--d1 [data-stale]")).toBeVisible();
  await expect(d1).toHaveValue("10005");
  await page.locator("#drafts-row--d1 [data-stale] button").click();
  await expect(page.locator("#draft-input-d1")).toHaveValue("4007");
  await expect(page.locator("#drafts-row--d1 [data-stale]")).toBeHidden();
});

test("3 editor: saving your own row is not treated as someone else's change", async ({ page }) => {
  await open(page, "/drafts");
  const d3 = page.locator("#draft-input-d3");
  await d3.fill("42");
  await page.locator("#drafts-row--d3 button", { hasText: "Save" }).click();
  await expect(page.locator("#drafts-row--d3")).toContainText("last saved by you");
  await settle(page);
  await expect(page.locator("#drafts-row--d3 [data-stale]")).toBeHidden();
  await expect(d3).toHaveValue("42");
});

test("3 per viewer: the stream renders who saved a row for each viewer", async ({ page, browser }) => {
  const otherContext = await browser.newContext();
  const other = await otherContext.newPage();
  await open(page, "/drafts");
  await open(other, "/drafts");
  await page.locator("#draft-input-d3").fill("42");
  await page.locator("#drafts-row--d3 button", { hasText: "Save" }).click();
  await expect(page.locator("#drafts-row--d3")).toContainText("last saved by you");
  await expect(other.locator("#draft-input-d3")).toHaveValue("42");
  await expect(other.locator("#drafts-row--d3")).toContainText(/last saved by viewer-\w+/);
  await expect(other.locator("#drafts-row--d3")).not.toContainText("last saved by you");
  await otherContext.close();
});

test("3 a focused, unedited field in a row that changes keeps focus", async ({ page }) => {
  await open(page, "/drafts");
  const d2 = page.locator("#draft-input-d2");
  await d2.focus();
  await sim(page, "Someone edits Operating cost");
  await expect(page.locator("#drafts-row--d2")).toContainText("last saved by Grace");
  await expect(d2).toBeFocused();
  await expect(d2).toHaveValue("4007");
});

test("4 insert and delete: an upload appears in order, a deletion disappears", async ({ page }) => {
  await open(page, "/files");
  await expect(page.getByTestId("file-count")).toHaveText("3");
  await sim(page, "Someone uploads a file");
  await expect(page.getByTestId("file-count")).toHaveText("4");
  await expect(page.getByTestId("files").locator("li")).toHaveCount(4);
  const names = await page.getByTestId("files").locator("li > span:first-child").allTextContents();
  expect(names).toEqual([...names].sort());
  await sim(page, "Someone deletes minutes.docx");
  await expect(page.locator("#files-item--f2")).toHaveCount(0);
  await expect(page.getByTestId("file-count")).toHaveText("3");
});

test("5 burst: fifty changes in 100 ms all land", async ({ page }) => {
  await open(page, "/burst");
  await sim(page, "50 rows change within 100 ms");
  await expect(page.locator("#burst-row--b50 [data-testid=value]")).toHaveText("150");
  await expect(page.locator("#burst-row--b1 [data-testid=value]")).toHaveText("101");
  await expect(page.locator("#burst-row--b51 [data-testid=value]")).toHaveText("51");
  await settle(page);
  const values = await page.locator("[data-testid=burst] [data-testid=value]").allTextContents();
  expect(values.slice(0, 50)).toEqual(Array.from({ length: 50 }, (_, i) => String(i + 101)));
  const m = await meter(page);
  expect(m.requests).toBe(0);
  expect(m.frames).toBeLessThanOrEqual(3);
});

test("6 nested levels: a progress tick patches the bar; a status change patches the card once", async ({ page }) => {
  await open(page, "/jobs");
  await sim(page, "Job j1 progresses");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-progress]")).toHaveAttribute("data-progress", "20");
  await settle(page);
  expect((await meter(page)).swaps).toBe(1);
  await sim(page, "Job j1 changes status");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-status]")).toHaveText("done");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-step]")).toHaveText("finished");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-progress]")).toHaveAttribute("data-progress", "100");
  await settle(page);
  expect((await meter(page)).swaps).toBe(2);
});

test("resync: nothing is pushed on connect when nothing changed since the render", async ({ page }) => {
  await page.goto("/series");
  await page.waitForFunction(() => (window as any).__meter?.connects > 0);
  await settle(page);
  const m = await meter(page);
  expect([m.frames, m.requests]).toEqual([0, 0]);
});

test("reconnect: a change made while the stream was down arrives on reconnect", async ({ page }) => {
  await open(page, "/series");
  await post(page, "/sim/drop-streams");
  await post(page, "/sim/series-touch", "s3");
  await expect(page.locator("#series-row--s3 [data-testid=points]")).toHaveText("307", { timeout: 10_000 });
  expect((await meter(page)).connects).toBeGreaterThan(1);
});
