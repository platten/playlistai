// Dependency-free static checks; rendered checks live in capture-site.mjs.
// Run: node --test scripts/test-site.mjs
import assert from "node:assert/strict";
import { readFileSync, existsSync } from "node:fs";
import { test } from "node:test";

const root = new URL("../", import.meta.url);
const html = readFileSync(new URL("site/index.html", root), "utf8");
const links = [...html.matchAll(/\bhref="([^"]+)"/g)].map(match => match[1]);

test("upcoming 0.8.0 is distinct from the public 0.7.0 download", () => {
  assert.match(html, /COMING IN VERSION 0\.8\.0/);
  assert.match(html, /PUBLIC DOWNLOAD \/ 0\.7\.0/);
  assert.match(html, /Version 0\.8\.0 is awaiting publication/);
  assert.ok(links.includes("https://github.com/platten/playlistai/blob/v0.8.0/docs/releases/v0.8.0.md"));
  assert.ok(!links.some(link => link.includes("untagged-") || link.includes("/releases/tag/v0.8.0")));
});

test("downloads retain exact published application asset names", () => {
  const approved = new Set(readFileSync(new URL("build/release-assets.txt", root), "utf8").trim().split(/\r?\n/));
  const downloads = links.filter(link => link.includes("/releases/download/"));
  assert.equal(downloads.length, 13);
  assert.equal(new Set(downloads).size, downloads.length);
  for (const link of downloads) {
    assert.ok(link.startsWith("https://github.com/platten/playlistai/releases/download/v0.7.0/"));
    assert.ok(approved.has(link.split("/").at(-1)), link);
  }
});

test("local navigation, accessibility references and files exist", () => {
  const ids = [...html.matchAll(/\bid="([^"]+)"/g)].map(match => match[1]);
  assert.equal(new Set(ids).size, ids.length, "duplicate element IDs");
  const references = [...html.matchAll(/\baria-(?:labelledby|controls)="([^"]+)"/g)].flatMap(match => match[1].split(" "));
  for (const id of [...references, ...links.filter(link => link.startsWith("#")).map(link => link.slice(1))]) {
    assert.ok(ids.includes(id), `Missing target: ${id}`);
  }
  for (const link of links.filter(link => !/^(https:|#)/.test(link))) {
    assert.ok(existsSync(new URL(`site/${link}`, root)), link);
  }
  for (const match of html.matchAll(/\bsrc="([^"]+)"/g)) {
    assert.ok(existsSync(new URL(`site/${match[1]}`, root)), match[1]);
  }
});

test("documentation links refer to existing files", () => {
  for (const link of links) {
    const match = link.match(/\/blob\/(?:v0\.8\.0|main)\/([^#]+)(?:#.*)?$/);
    if (match) assert.ok(existsSync(new URL(match[1], root)), link);
  }
});

test("release measurements retain their evidence and limitations", () => {
  const notes = readFileSync(new URL("docs/releases/v0.8.0.md", root), "utf8");
  for (const value of ["92.6 MB", "312.8 MB", "586,161", "956,917", "1.182", "89 µs"]) {
    assert.ok(html.includes(value), value);
    assert.ok(notes.includes(value), `Missing source measurement: ${value}`);
  }
  assert.match(html, /not proof of musical fit/);
  assert.match(html, /not typical playlist generation time/);
});

test("development modes stay separate from public release claims", () => {
  const section = html.match(/<section[^>]+id="recommendations"[\s\S]*?<\/section>/)?.[0];
  assert.ok(section, "Recommendation controls section missing");
  assert.match(section, /DEVELOPMENT PREVIEW \/ NOT IN THE PUBLIC DOWNLOAD/);
  for (const name of ["AcousticBrainz first", "CLAP first", "Deej-AI only"]) {
    assert.ok(section.includes(name), name);
  }
  assert.match(section, /Needs a resolved catalog artist or track/);
  assert.match(section, /Priority changes ranking, not permission to ignore exclusions/);
  assert.match(section, /changing Settings does not rewrite history/);
});

test("live counts agree with all three recorded regression runs", () => {
  const report = JSON.parse(readFileSync(new URL("docs/data/three-mode-regression-2026-09-09.json", root)));
  assert.equal(report.results.length, 120);
  assert.equal(report.summaries.length, 3);
  const rows = [...html.matchAll(/<tr data-mode="([^"]+)">([\s\S]*?)<\/tr>/g)];
  assert.equal(rows.length, 3);
  assert.equal(new Set(rows.map(row => row[1])).size, 3);
  for (const [, mode, row] of rows) {
    const summary = report.summaries.find(item => item.mode === mode);
    assert.ok(summary, mode);
    const cases = report.results.filter(item => item.mode === mode);
    assert.equal(cases.length, summary.cases);
    assert.equal(cases.filter(item => item.tracks.length >= 5).length, summary.atLeastFive);
    assert.equal(cases.filter(item => item.errors.length === 0).length, summary.assertionPasses);
    assert.ok(row.includes(`data-result="minimum">${summary.atLeastFive}/${summary.cases}</td>`), mode);
    assert.ok(row.includes(`data-result="assertions">${summary.assertionPasses}/${summary.cases}</td>`), mode);
  }
});

test("live measurements preserve failure and comparability caveats", () => {
  assert.match(html, /not listening-quality scores/);
  assert.match(html, /No mode met the five-track target for every prompt/);
  assert.match(html, /12 fulfilled playlists, 27 partial results/);
  assert.match(html, /David Bowie → Talking Heads/);
  assert.match(html, /not a controlled speed comparison/);
  assert.match(html, /no selected-track AcousticBrainz comparison scores/);
  assert.match(html, /No held-out listening judgments were used/);
  assert.ok(links.includes("https://github.com/platten/playlistai/blob/main/docs/three-mode-regression.md"));
  const benchmark = readFileSync(new URL("docs/performance-and-model-evaluation.md", root), "utf8");
  for (const value of ["1.888 s", "0.325 s", "5.8×"]) {
    assert.ok(html.includes(value), value);
    assert.ok(benchmark.includes(value), `Missing benchmark source: ${value}`);
  }
  assert.match(html, /not a whole-app speedup claim/);
});
