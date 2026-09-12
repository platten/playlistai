// Run Go tests with durable timing evidence without hiding their exit status.
import { spawn, spawnSync } from "node:child_process";
import { mkdirSync, createWriteStream, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { availableParallelism, cpus, totalmem } from "node:os";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { once } from "node:events";

export function timingCollector() {
  const packages = new Map();
  const tests = [];
  return {
    accept(event) {
      if (!event.Package) return;
      const item = packages.get(event.Package) ?? { package: event.Package };
      packages.set(event.Package, item);
      if (event.Action === "start" && !event.Test) item.startedAt = event.Time;
      if (["pass", "fail", "skip"].includes(event.Action)) {
        if (event.Test) tests.push({ package: event.Package, test: event.Test, outcome: event.Action, seconds: event.Elapsed ?? 0 });
        else Object.assign(item, { outcome: event.Action, seconds: event.Elapsed ?? 0, endedAt: event.Time });
      }
    },
    result() {
      return { packages: [...packages.values()].sort((a, b) => a.package.localeCompare(b.package)), tests: tests.sort((a, b) => b.seconds - a.seconds) };
    },
  };
}

export async function runTests({ report, args, command = "go", prefix = [] }) {
  mkdirSync(dirname(report), { recursive: true });
  const output = createWriteStream(`${report}.jsonl`);
  // Prevent an unhandled stream error; disk-full failures still fail the run.
  let outputError;
  output.on("error", error => { outputError = error; });
  const collector = timingCollector();
  const environment = command === "go" ? spawnSync("go", ["env", "-json", "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "CC", "GOMAXPROCS", "GOCACHE", "GOMODCACHE"], { encoding: "utf8" }) : null;
  const startedAt = new Date().toISOString();
  const started = performance.now();
  const child = spawn(command, [...prefix, "test", "-json", ...args], { stdio: ["ignore", "pipe", "inherit"] });
  const recent = new Map();
  let spawnError;
  child.on("error", error => { spawnError = error.message; });
  const completion = new Promise(resolve => child.on("close", (code, signal) => resolve({ code, signal })));
  const cancel = signal => child.kill(signal);
  const interrupt = () => cancel("SIGINT");
  const terminate = () => cancel("SIGTERM");
  process.on("SIGINT", interrupt);
  process.on("SIGTERM", terminate);
  try {
    for await (const line of createInterface({ input: child.stdout, crlfDelay: Infinity })) {
      if (!outputError && !output.write(`${line}\n`)) {
        await once(output, "drain").catch(error => { outputError = error; });
      }
      let event;
      try { event = JSON.parse(line); } catch { console.log(line); continue; }
      collector.accept(event);
      if (event.Action === "output") {
        const lines = recent.get(event.Package) ?? [];
        lines.push(event.Output);
        if (lines.length > 200) lines.shift();
        recent.set(event.Package, lines);
      }
      if (["pass", "fail", "skip"].includes(event.Action) && !event.Test) {
        if (event.Action === "fail") process.stdout.write((recent.get(event.Package) ?? []).join(""));
        console.log(`${event.Action}\t${event.Package}\t${event.Elapsed ?? 0}s`);
        recent.delete(event.Package);
      }
    }
    const { code, signal } = await completion;
    output.end();
    if (!output.closed) await once(output, "close");
    const exitCode = spawnError || outputError || signal ? 1 : (code ?? 1);
    let goEnvironment;
    try { goEnvironment = JSON.parse(environment?.stdout ?? "{}"); } catch { goEnvironment = {}; }
    const summary = {
      version: 1, startedAt, endedAt: new Date().toISOString(), seconds: (performance.now() - started) / 1000,
      exitCode, signal, error: spawnError ?? outputError?.message, args,
      host: { platform: process.platform, arch: process.arch, cpus: availableParallelism(), cpu: cpus()[0]?.model, memoryBytes: totalmem(), image: process.env.ImageOS, imageVersion: process.env.ImageVersion },
      github: { sha: process.env.GITHUB_SHA, event: process.env.GITHUB_EVENT_NAME, run: process.env.GITHUB_RUN_ID },
      go: goEnvironment, ...collector.result(),
    };
    writeFileSync(`${report}.summary.json`, `${JSON.stringify(summary, null, 2)}\n`);
    console.log(`Go tests: ${summary.seconds.toFixed(2)}s, exit ${exitCode}; timings: ${report}.summary.json`);
    for (const test of summary.tests.slice(0, 10)) console.log(`  ${test.seconds.toFixed(3)}s ${test.package}/${test.test}`);
    return exitCode;
  } finally {
    process.off("SIGINT", interrupt);
    process.off("SIGTERM", terminate);
    if (!output.closed) output.end();
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [report, ...args] = process.argv.slice(2);
  if (!report || args.length === 0) {
    console.error("Usage: node scripts/go-test.mjs REPORT_PREFIX GO_TEST_ARGS...");
    process.exitCode = 2;
  } else {
    process.exitCode = await runTests({ report, args });
  }
}
