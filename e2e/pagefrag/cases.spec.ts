import { test, expect, type Page } from "@playwright/test";
import { open, meter, sim, settle, record, messages } from "./helpers";

test.beforeEach(async ({ request }) => {
  await request.post("/sim/reset");
});

const post = (page: Page, action: string, id = "") =>
  page.evaluate(([a, i]) => fetch(`/sim/${a}`, { method: "POST", body: new URLSearchParams({ id: i }) }), [action, id]);

test("1 signal + pull, unchanged: one row change refetches that row and the tile in one GET", async ({ page }) => {
  await open(page, "/series");
  await sim(page, "Change series s3");
  await expect(page.locator("#series-row--s3 [data-testid=points]")).toHaveText("307");
  await expect(page.getByTestId("sum-points")).toHaveText("5507");
  await settle(page);
  const m = await meter(page);
  expect(m.urls).toEqual(["/series"]);
  expect(m.swaps).toBe(2);
});

test("2 value frames: one presence change reaches sidebar, directory and card with no request", async ({ page }) => {
  await open(page, "/people");
  const places = [
    page.getByTestId("dm-p1").locator("[data-live-value]"),
    page.getByTestId("row-p1").locator("span[data-value]"),
    page.getByTestId("card-p1").locator("span[data-value]"),
  ];
  for (const dot of places) await expect(dot).toHaveAttribute("data-value", "active");
  await sim(page, "Ada changes presence");
  for (const dot of places) await expect(dot).toHaveAttribute("data-value", "idle");
  await expect(page.getByTestId("row-p1").locator("[data-live-value-into=text]")).toHaveText("idle");
  await expect(page.getByTestId("card-p1")).toContainText("Currently idle");
  // CSS styles the attribute: the dot is amber now.
  expect(await places[0].evaluate((el) => getComputedStyle(el).backgroundColor)).not.toBe(
    await page.getByTestId("dm-p3").locator("[data-live-value]").evaluate((el) => getComputedStyle(el).backgroundColor),
  );
  await settle(page);
  const m = await meter(page);
  expect(m.requests).toBe(0);
  expect(m.swaps).toBe(0);
  expect(m.set).toBe(5); // three dots and two labels
  await expect(page.getByTestId("dm-p2").locator("[data-live-value]")).toHaveAttribute("data-value", "idle");
});

test("2 value frames: fifty people change at once, every one of their 200 listeners follows", async ({ page }) => {
  await open(page, "/people");
  const before = await page.locator('[data-live-value-key="c7"]').evaluateAll((els) => els.map((e) => e.getAttribute("data-value") ?? e.textContent));
  expect(before).toEqual(["idle", "idle", "idle", "idle"]); // strip, directory dot and label, card
  await sim(page, "All fifty members change presence");
  await expect(page.locator('[data-testid=crowd-strip] [data-value="inactive"]')).toHaveCount(17);
  await settle(page);
  const after = await page.locator('[data-live-value-key="c7"]').evaluateAll((els) => els.map((e) => e.getAttribute("data-value") ?? e.textContent));
  expect(after).toEqual(["inactive", "inactive", "inactive", "inactive"]);
  const m = await meter(page);
  expect(m.frames).toBe(1);
  expect(m.requests).toBe(0);
  expect(m.set).toBe(200);
});

test("3 fragment frames: a dirty row warns instead of morphing, a neighbour morphs without moving the caret", async ({ page }) => {
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
  await settle(page);
  expect((await meter(page)).requests).toBe(0);

  await sim(page, "Someone edits Revenue");
  await expect(page.locator("#drafts-row--d1 [data-live-stale]")).toBeVisible();
  await expect(d1).toHaveValue("10005");
  await page.locator("#drafts-row--d1 [data-live-stale] button").click();
  await expect(page.locator("#draft-input-d1")).toHaveValue("4007");
  await expect(page.locator("#drafts-row--d1 [data-live-stale]")).toBeHidden();
  await settle(page);
  expect((await meter(page)).urls).toEqual(["/drafts"]); // only the refresh pulled
});

test("3 fragment frames: saving your own row is not treated as someone else's change", async ({ page }) => {
  await open(page, "/drafts");
  const d3 = page.locator("#draft-input-d3");
  await d3.fill("42");
  await page.locator("#drafts-row--d3 button", { hasText: "Save" }).click();
  await expect(page.locator("#drafts-row--d3")).toContainText("last saved by you");
  await settle(page);
  await expect(page.locator("#drafts-row--d3 [data-live-stale]")).toBeHidden();
  await expect(d3).toHaveValue("42");
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

test("6 fragment frames: a progress tick pushes the bar, a status change pushes the card once, no requests", async ({ page }) => {
  await open(page, "/jobs");
  await sim(page, "Job j1 progresses");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-progress]")).toHaveAttribute("data-progress", "20");
  await settle(page);
  let m = await meter(page);
  expect([m.requests, m.swaps]).toEqual([0, 1]);
  await sim(page, "Job j1 changes status");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-status]")).toHaveText("done");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-step]")).toHaveText("finished");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-progress]")).toHaveAttribute("data-progress", "100");
  await settle(page);
  m = await meter(page);
  expect([m.requests, m.swaps]).toEqual([0, 2]);
});

test("fragments for elements the page does not show are ignored, with no request and no warning", async ({ page }) => {
  const warnings: string[] = [];
  page.on("console", (msg) => msg.type() === "warning" && warnings.push(msg.text()));
  await open(page, "/jobs");
  await page.locator("#jobs-card--j2").evaluate((el) => el.remove()); // a page that shows j1 only
  await post(page, "job-step", "j2");
  await post(page, "job-advance", "j2");
  await settle(page);
  const m = await meter(page);
  expect(m.frames).toBe(2); // heard: j1's card and bar listen to both topics
  expect([m.requests, m.swaps]).toEqual([0, 0]);
  expect(warnings).toEqual([]);
});

test("two viewers: a fragment rendered for one never reaches the other; value frames reach every listener of both", async ({ page, browser }) => {
  const otherContext = await browser.newContext();
  const other = await otherContext.newPage();
  await open(page, "/drafts");
  await open(other, "/drafts");
  const [me, them] = await Promise.all([page.getByTestId("viewer").textContent(), other.getByTestId("viewer").textContent()]);
  expect(me).not.toBe(them);
  await record(page);
  await record(other);

  await page.locator("#draft-input-d3").fill("42");
  await page.locator("#drafts-row--d3 button", { hasText: "Save" }).click();
  await expect(page.locator("#drafts-row--d3")).toContainText("last saved by you");
  await expect(other.locator("#draft-input-d3")).toHaveValue("42");
  await expect(other.locator("#drafts-row--d3")).toContainText(`last saved by ${me}`);
  await settle(page);
  await settle(other);
  const mine = (await messages(page)).filter((m) => m.event === "lab:draft").map((m) => m.data);
  const theirs = (await messages(other)).filter((m) => m.event === "lab:draft").map((m) => m.data);
  expect(mine).toHaveLength(1);
  expect(theirs).toHaveLength(1);
  expect(mine[0]).toContain("last saved by you");
  expect(theirs[0]).toContain(`last saved by ${me}`);
  expect(theirs[0]).not.toContain("last saved by you");
  expect((await meter(other)).requests).toBe(0);

  // Value frames: every listener of p1 in both viewers' tabs, the sidebar's included.
  await post(page, "presence", "p1");
  for (const p of [page, other]) {
    await expect(p.getByTestId("dm-p1").locator("[data-live-value]")).toHaveAttribute("data-value", "idle");
  }
  await other.goto("/people");
  await post(page, "presence", "p1");
  for (const loc of [other.getByTestId("dm-p1"), other.getByTestId("row-p1"), other.getByTestId("card-p1")]) {
    await expect(loc.locator("span[data-value]")).toHaveAttribute("data-value", "inactive");
  }
  await expect(page.getByTestId("dm-p1").locator("[data-live-value]")).toHaveAttribute("data-value", "inactive");
  await otherContext.close();
});

test("revoking a viewer's access stops the very next fragment: the page's middleware runs on every push", async ({ page, browser }) => {
  const otherContext = await browser.newContext();
  const other = await otherContext.newPage();
  await open(page, "/jobs");
  await open(other, "/jobs");
  const them = await other.getByTestId("viewer").textContent();
  await record(page);
  await record(other);

  await post(page, "job-advance", "j1"); // both still have access: both are pushed the bar
  for (const p of [page, other]) {
    await expect(p.locator("#jobs-card--j1 [data-testid=job-progress]")).toHaveAttribute("data-progress", "20");
  }
  await post(page, "revoke-jobs", them!);
  await page.waitForTimeout(300); // past the hub's coalescing gap, so the next frame goes at once
  await post(page, "job-step", "j1");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-status]")).toHaveText("done");
  await settle(page);
  await settle(other);

  const theirs = (await messages(other)).filter((m) => m.event === "lab:job");
  expect(theirs.map((m) => JSON.parse(m.data))).toEqual([{ ids: ["j1"] }]); // the bare signal: no HTML
  const progress = (await messages(other)).filter((m) => m.event === "lab:job-progress");
  expect(JSON.parse(progress[0].data).fragments).toHaveLength(1); // before the revocation
  await expect(other.locator("#jobs-card--j1 [data-testid=job-status]")).toHaveText("running");
  const m = await meter(other);
  expect(m.urls).toEqual(["/jobs"]); // it pulled, and the page refused that too
  expect(m.swaps).toBe(1); // only the bar, before the revocation
  await otherContext.close();
});

test("a fragment the page answers 404 for removes the element, with no request", async ({ page }) => {
  await open(page, "/jobs");
  await page.evaluate(() => {
    const card = document.querySelector("#jobs-card--j2")!;
    const ghost = card.cloneNode(true) as HTMLElement;
    ghost.id = "jobs-card--gone";
    ghost.dataset.liveKey = "gone";
    ghost.querySelectorAll("[id]").forEach((el) => (el.id = el.id.replace("--j2", "--gone")));
    card.after(ghost);
  });
  await post(page, "job-gone", "gone"); // a job deleted elsewhere: the page answers 404 for it
  await expect(page.locator("#jobs-card--gone")).toHaveCount(0);
  await settle(page);
  expect((await meter(page)).requests).toBe(0);
});

test("resync: values and fragments missed while the stream was down converge on reconnect", async ({ page }) => {
  await open(page, "/people");
  await post(page, "drop-streams");
  await post(page, "presence", "p1");
  await post(page, "presence-crowd");
  await expect(page.getByTestId("card-p1").locator("span[data-value]")).toHaveAttribute("data-value", "idle", { timeout: 10_000 });
  await expect(page.locator('[data-live-value-key="c7"][data-value]').first()).toHaveAttribute("data-value", "inactive");
  expect((await meter(page)).requests).toBe(0); // the values came with the resync

  await page.goto("/jobs");
  await page.waitForTimeout(500);
  await post(page, "drop-streams");
  await post(page, "job-step", "j1");
  await expect(page.locator("#jobs-card--j1 [data-testid=job-status]")).toHaveText("done", { timeout: 10_000 });
});

test("resync: nothing is sent on connect when nothing changed since the render", async ({ page }) => {
  await page.goto("/people");
  await settle(page);
  const m = await meter(page);
  expect([m.frames, m.requests, m.set]).toEqual([0, 0, 0]);
});
