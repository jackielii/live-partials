import { test, expect, type Page } from "@playwright/test";
import { open, meter, sim, settle } from "./helpers";

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
  }
});

test("2 same key in many places: a presence change reaches sidebar, directory and card", async ({ page }) => {
  await open(page, "/people");
  await watch(page, "[data-live-key]");
  await sim(page, "Ada changes presence");
  for (const sel of ["#shell-dm--p1", "#people-row--p1", "#people-card--p1"]) {
    await expect(page.locator(`${sel} [data-presence]`).first()).toHaveAttribute("data-presence", "idle");
  }
  await settle(page);
  expect((await changed(page)).filter((id) => !id.endsWith("p1"))).toEqual([]);
});

test("3 editor: a dirty row warns instead of swapping, a neighbour swaps without moving the caret", async ({ page }) => {
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
  await expect(d1).toBeFocused();
  expect(await d1.evaluate((el: HTMLInputElement) => el.selectionStart)).toBe(caret);
  await expect(d1).toHaveValue("10005");

  await sim(page, "Someone edits Revenue");
  await expect(page.locator("#drafts-row--d1 [data-live-stale]")).toBeVisible();
  await expect(d1).toHaveValue("10005");
  await page.locator("#drafts-row--d1 [data-live-stale] button").click();
  await expect(page.locator("#draft-input-d1")).toHaveValue("4007");
  await expect(page.locator("#drafts-row--d1 [data-live-stale]")).toBeHidden();
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
});

test("6 nested levels: a progress tick updates the bar; a status change updates the card", async ({ page }) => {
  await open(page, "/jobs");
  await sim(page, "Job j1 progresses");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-progress]")).toHaveAttribute("data-progress", "20");
  await sim(page, "Job j1 changes status");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-status]")).toHaveText("done");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-step]")).toHaveText("finished");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-progress]")).toHaveAttribute("data-progress", "100");
});
