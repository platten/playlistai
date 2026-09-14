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
window.__calls=[];window.__done=0;window.__installed=false;
const scenario=new URLSearchParams(location.search).get('scenario');
const methods={
GetSetupStatus:()=>scenario==='repair'?{onboarded:true,needsSetup:true,pendingSteps:['model','intent','analysis'],repairSteps:['model']}:
 {onboarded:false,needsSetup:true,pendingSteps:scenario==='mert'?['mert']:scenario==='partial'&&!window.__installed?['metadata']:[],repairSteps:[]},
GetEnhancedAnalysisStatus:()=>({installed:window.__installed,dspAvailable:true,enabled:true,mertEnabled:true,mertAvailable:window.__installed,searchableTracks:0,recommendedManifestUrl:'https://models.example/mert/manifest.json',recommendedDownloadBytes:213882011}),
InstallRecommendedMERT:()=>{window.__installed=true},
GetMetadataBundleInfo:()=>({musicBrainzConfigured:true,musicBrainzInstalled:window.__installed}),
InstallMusicBrainzBundle:()=>{window.__installed=true},
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
      await page.getByRole("button", { name: "Download MERT from Cloudflare R2" }).waitFor();
      assert.equal(await page.getByRole("checkbox").count(), 0);
      assert.equal(await page.getByRole("button", { name: "Continue" }).isDisabled(), true);
      await page.getByRole("button", { name: "Download MERT from Cloudflare R2" }).click();
      await page.getByText("0 tracks with compatible cached embeddings.").waitFor();
      assert.equal(await page.getByRole("button", { name: "Continue" }).isEnabled(), true);
    } else if (scenario === "partial") {
      await page.getByRole("button", { name: "Get started" }).click();
      await page.getByRole("button", { name: "Download offline music data" }).waitFor();
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
    if (scenario === "repair") {
      assert.equal(await page.getByRole("button", { name: "Skip for now" }).count(), 0);
      assert.equal(await page.getByRole("button", { name: "Continue" }).isDisabled(), true);
      continue;
    }
    if (scenario === "partial") await page.getByRole("button", { name: "Download offline music data" }).click();
    if (scenario === "mert") await page.getByRole("button", { name: "Continue" }).click();
    const finish = page.getByRole("button", { name: "Start using Playlist AI" });
    await finish.waitFor();
    await finish.focus();
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => window.__done === 1);
    const calls = await page.evaluate(() => window.__calls);
    assert.ok(!calls.includes("SetPreviewProvider"), "ready or unselected steps must not run");
    assert.equal(calls.filter(call => call === "InstallRecommendedMERT").length, scenario === "mert" ? 1 : 0);
    assert.equal(calls.filter(call => call === "CompleteOnboarding").length, 1);
  }
  assert.deepEqual(errors, []);
  console.log("PASS: required ready/repair/partial/MERT setup; dark/light 390/1000; keyboard finish; no redundant asset work");
} finally { await browser.close(); }
