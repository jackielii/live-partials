import { test, expect } from "@playwright/test";
import { open, meter, settle } from "./helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/sim/reset");
});

const post = (page, action: string, id = "") =>
  page.evaluate(([a, i]) => fetch(`/sim/${a}`, { method: "POST", body: new URLSearchParams({ id: i }) }), [action, id]);

test("a frame costs one GET per page URL however many elements it touches", async ({ page }) => {
  await open(page, "/people");
  await post(page, "presence", "p1");
  await settle(page);
  const m = await meter(page);
  // The directory row and card are the page's; the sidebar row is the shell's (data-live-url).
  expect([...m.urls].sort()).toEqual(["/people", "/shell"]);
  expect(m.swaps).toBe(3);
});

test("three rows in one frame are one GET and one row query", async ({ page }) => {
  await open(page, "/series");
  await post(page, "series-touch", "s2 s5 s8");
  await expect(page.locator("#series-row--s8 [data-testid=points]")).toHaveText("807");
  await settle(page);
  const m = await meter(page);
  expect(m.urls).toEqual(["/series"]);
  expect(m.swaps).toBe(4); // three rows and the summary
  expect(m.queries).toBe(2); // live.Load: one for the rows, one for the summary
});

test("outer wins: a status change refetches the job card, not its step line too", async ({ page }) => {
  await open(page, "/jobs");
  await post(page, "job-step", "j1");
  await settle(page);
  const m = await meter(page);
  expect(m.requests).toBe(1);
  expect(m.swaps).toBe(1);
});

test("a burst of fifty escalates to the table and stays within a few requests", async ({ page }) => {
  await open(page, "/burst");
  await post(page, "burst");
  await expect(page.locator("#burst-row--b50 [data-testid=value]")).toHaveText("150");
  await settle(page);
  const m = await meter(page);
  expect(m.requests).toBeLessThanOrEqual(3);
  expect(m.swaps).toBeLessThanOrEqual(3);
});

test("frames no element listens to are dropped before htmx dispatches them", async ({ page }) => {
  await open(page, "/files");
  await post(page, "series-touch", "s1");
  await page.waitForTimeout(500);
  const m = await meter(page);
  expect(m.frames).toBe(0);
  expect(m.requests).toBe(0);
});

test("a deleted row is removed by the 404 its refetch gets", async ({ page }) => {
  await open(page, "/files");
  await post(page, "file-delete", "f2");
  await expect(page.locator("#files-item--f2")).toHaveCount(0);
  await settle(page);
  expect((await meter(page)).urls).toEqual(["/files"]);
});

test("a focused, unedited field in a row that changes keeps focus", async ({ page }) => {
  await open(page, "/drafts");
  const d2 = page.locator("#draft-input-d2");
  await d2.focus();
  await post(page, "draft", "d2");
  await expect(page.locator("#drafts-row--d2")).toContainText("last saved by Grace");
  await expect(d2).toBeFocused();
  await expect(d2).toHaveValue("4007");
});

test("reconnect: a change made while the stream was down arrives on reconnect", async ({ page }) => {
  await open(page, "/series");
  await post(page, "drop-streams");
  await post(page, "series-touch", "s3");
  await expect(page.locator("#series-row--s3 [data-testid=points]")).toHaveText("307", { timeout: 10_000 });
});
