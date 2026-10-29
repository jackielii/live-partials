import { test } from "@playwright/test";
import { writeFileSync, mkdirSync } from "node:fs";
import { open, meter, sim, settle, type Meter } from "./helpers";

// Not an assertion suite: records what each case's action costs one tab and
// writes it to test-results/measure-<LAB_VARIANT>.json for docs/findings.md.
const rows: Record<string, Omit<Meter, "urls"> & { urls: number }> = {};

const cases: [string, string, string[]][] = [
  ["1 one row of ten", "/series", ["Change series s3"]],
  ["1 three rows in one frame", "/series", ["Change series s2, s5 and s8 together"]],
  ["2 one person, three places", "/people", ["Ada changes presence"]],
  ["3 neighbour row edited", "/drafts", ["Someone edits Operating cost"]],
  ["4 upload, then delete", "/files", ["Someone uploads a file", "Someone deletes minutes.docx"]],
  ["5 burst of 50", "/burst", ["50 rows change within 100 ms"]],
  ["6 progress tick", "/jobs", ["Job j1 progresses"]],
  ["6 status change", "/jobs", ["Job j1 changes status"]],
  ["resync on connect", "/series", []],
];

test.afterAll(() => {
  console.table(rows);
  mkdirSync("test-results", { recursive: true });
  writeFileSync(`test-results/measure-${process.env.LAB_VARIANT ?? "current"}.json`, JSON.stringify(rows, null, 2));
});

for (const [name, path, labels] of cases) {
  test(`measure ${name}`, async ({ page, request }) => {
    await request.post("/sim/reset");
    if (labels.length === 0) {
      await page.goto(path);
      await settle(page);
    } else {
      await open(page, path);
      for (const l of labels) {
        await sim(page, l);
        await settle(page);
      }
    }
    const m = await meter(page);
    rows[name] = { ...m, server: +m.server.toFixed(2), swapms: +m.swapms.toFixed(2), urls: new Set(m.urls).size };
  });
}
