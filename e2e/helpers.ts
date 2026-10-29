import { expect, type Page } from "@playwright/test";

export type Meter = { frames: number; requests: number; bytes: number; queries: number; swaps: number; server: number; swapms: number; urls: string[] };

// open loads a case and waits until the connect-time resync has settled, then
// zeroes the meter so a test reads only what its own action cost.
export async function open(page: Page, path: string) {
  await page.goto(path);
  await page.waitForFunction(() => (window as any).htmx && document.querySelector("#stream")?.["_htmx"]?.sse);
  await page.waitForTimeout(300);
  await page.click("[data-meter-reset]");
}

export const meter = (page: Page): Promise<Meter> => page.evaluate(() => structuredClone((window as any).__meter));

// sim presses a simulate button's action without clicking it, so the
// "other user" never takes focus from the field the test is typing in.
export async function sim(page: Page, label: string) {
  const btn = page.getByRole("button", { name: label });
  const [url, vals] = await Promise.all([btn.getAttribute("hx-post"), btn.getAttribute("hx-vals")]);
  await page.evaluate(
    ([u, v]) => fetch(u!, { method: "POST", body: new URLSearchParams(JSON.parse(v || "{}")) }).then((r) => r.status),
    [url, vals],
  );
}

// settle waits until the meter has stopped moving for quietMs.
export async function settle(page: Page, quietMs = 400) {
  let last = "";
  for (let i = 0; i < 50; i++) {
    const now = JSON.stringify(await meter(page));
    if (now === last) return;
    last = now;
    await page.waitForTimeout(quietMs);
  }
  expect(false, "meter never settled").toBe(true);
}
