import { type Page } from "@playwright/test";
import { open, sim, settle } from "../helpers";
export { open, sim, settle };

export type Meter = {
  frames: number;
  requests: number;
  bytes: number;
  queries: number;
  swaps: number;
  set: number;
  server: number;
  swapms: number;
  urls: string[];
};

export const meter = (page: Page): Promise<Meter> => page.evaluate(() => structuredClone((window as any).__meter));

// record keeps every stream message the page receives from now on, as the
// stream sent it, for checking what reached which viewer.
export async function record(page: Page) {
  await page.evaluate(() => {
    const w = window as any;
    w.__messages = [];
    document.addEventListener("htmx:sse:before:message", (e: any) => w.__messages.push(e.detail.message));
  });
}

export const messages = (page: Page): Promise<{ event: string; data: string }[]> =>
  page.evaluate(() => (window as any).__messages);
