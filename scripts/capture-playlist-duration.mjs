// Render the actual duration summary with deterministic public fixtures.
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
try {
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {PlaylistDuration} from '/src/components/PlaylistDuration.tsx';import '/src/design/tokens.css';
    const state=new URLSearchParams(location.search).get('state');const duration={targetSeconds:4500,toleranceSeconds:60,knownMilliseconds:state==='mismatch'?4600000:4500000,unknownTrackIds:state==='unknown'?['missing']:[],state};
    ReactDOM.createRoot(document.getElementById('root')).render(React.createElement('section',{style:{padding:24,maxWidth:760}},React.createElement('h1',null,'Playlist duration'),React.createElement(PlaylistDuration,{duration})));`;
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, (r) => r.fulfill({ contentType: "application/javascript", body: entry }));
  for (const state of ["match", "mismatch", "unknown"]) {
    await page.goto(`http://127.0.0.1:9245/?state=${state}`);
    await page.getByRole("status").waitFor();
    const text = await page.getByRole("status").innerText();
    assert.ok(text.includes("Target 75:00 ±60 seconds."));
    assert.ok(text.includes(state === "match" ? "Within the requested range." : state === "mismatch" ? "Outside the requested range." : "Total duration is unverified."));
    for (const theme of ["dark", "light"]) {
      await page.evaluate((t) => { document.documentElement.dataset.theme = t; }, theme);
      await page.setViewportSize({ width: 390, height: 500 });
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: path.join(output, `duration-${state}-${theme}.png`) });
    }
  }
  assert.deepEqual(errors, []);
  console.log("Rendered duration match/mismatch/unknown states passed at 390px in both themes.");
} finally { await browser.close(); }
