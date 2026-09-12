import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { timingCollector, runTests } from "./go-test.mjs";
import { setTimeout as delay } from "node:timers/promises";

test("overlapping package and subtest durations remain separate", () => {
  const collector = timingCollector();
  for (const event of [
    { Package: "a", Action: "start", Time: "2026-09-12T12:00:00Z" },
    { Package: "b", Action: "start", Time: "2026-09-12T12:00:00Z" },
    { Package: "a", Test: "TestParent/child", Action: "pass", Elapsed: 2 },
    { Package: "a", Test: "TestParent", Action: "pass", Elapsed: 3 },
    { Package: "a", Action: "pass", Elapsed: 4 },
    { Package: "b", Action: "fail", Elapsed: 3 },
  ]) collector.accept(event);
  const result = collector.result();
  assert.deepEqual(result.packages.map(p => [p.package, p.outcome, p.seconds]), [["a", "pass", 4], ["b", "fail", 3]]);
  assert.equal(result.tests[0].test, "TestParent");
});

for (const exitCode of [0, 7]) test(`child exit ${exitCode} survives timing collection`, async t => {
  const dir = mkdtempSync(join(tmpdir(), "playlist-ci-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const helper = join(dir, "fake-go.cjs");
  writeFileSync(helper, `console.log(JSON.stringify({Action:"${exitCode ? "fail" : "pass"}",Package:"fixture",Elapsed:0.1}));process.exitCode=${exitCode};`);
  const report = join(dir, "timings");
  assert.equal(await runTests({ report, args: ["./..."], command: process.execPath, prefix: [helper] }), exitCode);
  const summary = JSON.parse(readFileSync(`${report}.summary.json`, "utf8"));
  assert.equal(summary.exitCode, exitCode);
  assert.equal(summary.packages.length, 1);
  assert.ok(summary.seconds > 0);
  assert.match(readFileSync(`${report}.jsonl`, "utf8"), /fixture/);
});

test("missing executable fails and retains diagnostic summary", async t => {
  const dir = mkdtempSync(join(tmpdir(), "playlist-ci-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const report = join(dir, "timings");
  assert.equal(await runTests({ report, args: ["./..."], command: join(dir, "absent") }), 1);
  assert.match(JSON.parse(readFileSync(`${report}.summary.json`, "utf8")).error, /ENOENT/);
});

test("cancellation terminates the child and cannot report success", { timeout: 10000 }, async t => {
  const dir = mkdtempSync(join(tmpdir(), "playlist-ci-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const ready = join(dir, "ready");
  const helper = join(dir, "waiting-go.cjs");
  writeFileSync(helper, `require("node:fs").writeFileSync(${JSON.stringify(ready)}, "ready");setInterval(() => {}, 1000);`);
  const report = join(dir, "timings");
  const running = runTests({ report, args: ["./..."], command: process.execPath, prefix: [helper] });
  try {
    const deadline = Date.now() + 5000;
    while (!existsSync(ready) && Date.now() < deadline) await delay(10);
    assert.ok(existsSync(ready), "child never became ready");
  } finally {
    // Exercise the registered signal handler, not a timing-dependent kill.
    process.emit("SIGTERM");
    assert.equal(await running, 1);
  }
  assert.equal(JSON.parse(readFileSync(`${report}.summary.json`, "utf8")).signal, "SIGTERM");
});

test("an early failure remains visible after a package produces many later events", async t => {
  const dir = mkdtempSync(join(tmpdir(), "playlist-ci-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const helper = join(dir, "failing-go.cjs");
  writeFileSync(helper, `
    const emit = event => console.log(JSON.stringify({Package:"fixture", ...event}));
    emit({Action:"output",Test:"TestEarly",Output:"assertion details\\n"});
    emit({Action:"fail",Test:"TestEarly",Elapsed:0.01});
    for(let i=0;i<250;i++) emit({Action:"output",Test:"TestLater",Output:"later output\\n"});
    emit({Action:"pass",Test:"TestLater",Elapsed:0.01});
    emit({Action:"fail",Elapsed:0.02});
    process.exitCode=1;
  `);
  let visible = "";
  const report = join(dir, "timings");
  assert.equal(await runTests({ report, args: ["./..."], command: process.execPath, prefix: [helper], writeLog: text => { visible += text; } }), 1);
  assert.match(visible, /assertion details/);
  assert.match(readFileSync(`${report}.jsonl`, "utf8"), /assertion details/);
});
