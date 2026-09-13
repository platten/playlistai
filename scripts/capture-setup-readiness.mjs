// Actual wizard with local readiness fixtures; never downloads or changes user data.
// node scripts/capture-setup-readiness.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const fixture = `
window.__calls=[];window.__done=0;window.__installed=false;window.__enhancedEnabled=false;
const scenario=new URLSearchParams(location.search).get('scenario');
const methods={
GetSetupStatus:()=>scenario==='repair'?{onboarded:true,needsSetup:true,pendingSteps:['model','intent','analysis'],repairSteps:['model']}:
 {onboarded:false,needsSetup:true,pendingSteps:scenario==='mert'?['mert']:scenario==='partial'&&!window.__installed?['metadata']:[],repairSteps:[]},
SetEnhancedAnalysisEnabled:enabled=>{window.__enhancedEnabled=enabled},
GetEnhancedAnalysisStatus:()=>({installed:false,dspAvailable:true,enabled:window.__enhancedEnabled,recommendedManifestUrl:'https://models.example/mert/manifest.json',recommendedDownloadBytes:213882011}),
GetMetadataBundleInfo:()=>({configured:true,installed:window.__installed,catalogReady:true}),
InstallMetadataBundle:()=>{window.__installed=true},
GetModelStatus:()=>({backend:'rules',ready:true}),GetLlamaRuntime:()=>({available:false,builds:[]}),
GetInstalledModels:()=>[],GetModelRecommendations:()=>({models:[],hardware:{gpuAvailable:false}}),
CompleteOnboarding:()=>{},
};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{window.__calls.push(k);const p=Promise.resolve().then(()=>{if(!o[k])throw Error('Unexpected API '+k);return o[k](...args)});p.cancel=()=>{};return p;}});`;
const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {FirstRunWizard} from '/src/screens/FirstRunWizard.tsx';import '/src/design/tokens.css';ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(FirstRunWizard,{onDone:()=>window.__done++}));`;
const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1000, height: 760 } });
  page.on("pageerror", error => errors.push(error.message));
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: entry }));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: fixture }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: "export const Events={On:()=>()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;" }));
  for (const scenario of ["ready", "repair", "partial", "mert"]) {
    await page.goto(`http://127.0.0.1:9245/?scenario=${scenario}`);
    if (scenario === "mert") {
      await page.getByRole("button", { name: "Get started" }).click();
      await page.getByRole("heading", { name: "MERT audio similarity" }).waitFor();
      await page.getByRole("button", { name: "Download MERT for this device" }).waitFor();
      assert.equal(await page.getByRole("checkbox").isChecked(), false);
      await page.getByRole("checkbox",{name:"Enable bounded preview analysis"}).check();
      await page.waitForFunction(()=>window.__enhancedEnabled);
    } else if (scenario === "partial") {
      await page.getByRole("button", { name: "Get started" }).click();
      await page.getByRole("button", { name: "Download music metadata" }).waitFor();
    } else if (scenario === "repair") {
      await page.getByRole("heading", { name: "Install llama.cpp" }).waitFor();
      assert.equal(await page.getByRole("button", { name: "Get started" }).count(), 0);
    } else {
      await page.getByText("You're set up").waitFor();
      assert.equal(await page.getByRole("button", { name: "Get started" }).count(), 0);
      assert.deepEqual(await page.evaluate(() => window.__calls), ["GetSetupStatus"]);
    }
    for (const theme of ["dark", "light"]) {
      await page.evaluate(value => document.documentElement.dataset.theme = value, theme);
      for (const width of [1000, 390]) {
        await page.setViewportSize({ width, height: 760 });
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "horizontal overflow");
        await page.screenshot({ path: path.join(output, `setup-${scenario}-${theme}-${width}.png`), fullPage: true });
      }
    }
    if (scenario === "repair") await page.getByRole("button", { name: "Skip for now" }).click();
    if (scenario === "partial") await page.getByRole("button", { name: "Download music metadata" }).click();
    if (scenario === "mert") await page.getByRole("button", { name: "Continue" }).click();
    const finish = page.getByRole("button", { name: "Start using Playlist AI" });
    await finish.waitFor();
    await finish.focus();
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => window.__done === 1);
    const calls = await page.evaluate(() => window.__calls);
    assert.ok(!calls.includes("InstallIntentModels") && !calls.includes("SetPreviewProvider"), "ready or unselected optional steps must not run");
    assert.ok(!calls.includes("InstallRecommendedMERT"), "MERT requires an explicit download action");
    assert.equal(calls.filter(call => call === "CompleteOnboarding").length, 1);
  }
  assert.deepEqual(errors, []);
  console.log("PASS: ready/repair/partial setup; dark/light390/1000; keyboard finish; no redundant asset work");
} finally { await browser.close(); }
