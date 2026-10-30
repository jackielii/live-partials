import { expect, type Page } from "@playwright/test";
import type { Meter } from "../helpers";
export type { Meter };

// open loads a case, waits until its stream is open and anything it pushed on
// connect has settled, then zeroes the meter so a test reads only what its
// own action cost.
export async function open(page: Page, path: string) {
  await page.goto(path);
  await page.waitForFunction(() => (window as any).__meter?.connects > 0);
  await page.waitForTimeout(300);
  await page.click("[data-meter-reset]");
}

export const meter = (page: Page): Promise<Meter> => page.evaluate(() => structuredClone((window as any).__meter));

// sim presses a simulate button's action without clicking it, so the
// "other user" never takes focus from the field the test is typing in.
export async function sim(page: Page, label: string) {
  const btn = page.getByRole("button", { name: label });
  const [url, id] = await Promise.all([btn.getAttribute("data-url"), btn.getAttribute("data-sim-id")]);
  await post(page, url!, id ?? "");
}

export const post = (page: Page, url: string, id = "") =>
  page.evaluate(
    ([u, i]) => fetch(u, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ id: i }) }).then((r) => r.status),
    [url, id],
  );

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
