import { test, expect, type Page } from "@playwright/test";

// The case 8 journey, run against both apps: the filter lives in the URL,
// survives Back, Forward and reload, and live changes respect it.
export function catalogTests(open: (page: Page, path: string) => Promise<void>) {
  const touch = (page: Page, id: string) =>
    page.evaluate((i) => fetch("/sim/series-touch", { method: "POST", body: new URLSearchParams({ id: i }) }).then((r) => r.status), id);
  const filter = (page: Page, name: string) => page.getByTestId("filters").getByRole("link", { name, exact: true });
  const count = (page: Page) => page.getByTestId("catalog-count");
  const rows = (page: Page) => page.getByTestId("catalog").locator("tr");

  test.beforeEach(async ({ request }) => {
    await request.post("/sim/reset");
  });

  test("8 url state: the filter is in the URL, live changes respect it, Back and Forward restore it", async ({ page }) => {
    await open(page, "/catalog");
    await expect(count(page)).toHaveText("10");

    await filter(page, "failing").click();
    await expect(page).toHaveURL(/\/catalog\?status=failing$/);
    await expect(count(page)).toHaveText("0");

    // ok → stale → failing: s3 enters the filtered list.
    await touch(page, "s3");
    await touch(page, "s3");
    await expect(rows(page)).toHaveCount(1);
    await expect(page.locator("#catalog-row--s3 [data-testid=status]")).toHaveText("failing");
    await expect(count(page)).toHaveText("1");
    // failing → ok: it leaves.
    await touch(page, "s3");
    await expect(rows(page)).toHaveCount(0);
    await expect(count(page)).toHaveText("0");

    await filter(page, "ok").click();
    await expect(page).toHaveURL(/\?status=ok$/);
    await expect(count(page)).toHaveText("10");

    await page.goBack();
    await expect(page).toHaveURL(/\?status=failing$/);
    await expect(count(page)).toHaveText("0");
    // The stream follows the restored filter.
    await touch(page, "s7");
    await touch(page, "s7");
    await expect(page.locator("#catalog-row--s7 [data-testid=status]")).toHaveText("failing");
    await expect(count(page)).toHaveText("1");

    await page.goForward();
    await expect(page).toHaveURL(/\?status=ok$/);
    await expect(count(page)).toHaveText("9");
    await expect(page.locator("#catalog-row--s7")).toHaveCount(0);

    await page.reload();
    await expect(page).toHaveURL(/\?status=ok$/);
    await expect(count(page)).toHaveText("9");
  });

  test("8 url state: a shared filtered URL renders filtered on first load", async ({ page }) => {
    await open(page, "/catalog?status=stale");
    await expect(count(page)).toHaveText("0");
    await touch(page, "s1");
    await expect(page.locator("#catalog-row--s1 [data-testid=status]")).toHaveText("stale");
  });
}
