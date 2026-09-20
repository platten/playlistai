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
window.__calls=[];window.__done=0;window.__installed=false;window.__source='hosted';
const scenario=new URLSearchParams(location.search).get('scenario');
if(scenario==='discovery-settings'){window.__installed=true;window.__source='local';}
const methods={
GetSetupStatus:()=>scenario==='repair'?{onboarded:true,needsSetup:true,pendingSteps:['model','intent','analysis'],repairSteps:['model']}:
 {onboarded:scenario==='discovery',needsSetup:true,pendingSteps:scenario==='discovery'&&!window.__installed?['discovery']:scenario==='mert'?['mert']:scenario==='partial'&&!window.__installed?['metadata']:[],repairSteps:scenario==='discovery'&&!window.__installed?['discovery']:[]},
GetDiscoveryAssetStatus:()=>({configured:true,hostedConfigured:true,source:window.__source,installed:window.__installed,version:window.__installed?'fixture-v1':'',tracks:window.__installed?420:0,downloadBytes:0,packIds:[]}),
CheckDiscoveryAssetUpdate:()=>({version:'fixture-v1',downloadBytes:1200000000,updateAvailable:!window.__installed}),
InstallDiscoveryAsset:()=>{window.__installed=true;window.__source='hosted';return {configured:true,hostedConfigured:true,source:'hosted',installed:true,version:'fixture-v1',tracks:420,downloadBytes:1200000000,packIds:[]}},
ChooseDiscoveryPack:()=>{window.__installed=true;window.__source='local';return {canceled:false,status:{configured:true,hostedConfigured:true,source:'local',installed:true,version:'local-fixture',tracks:8,downloadBytes:0,packIds:[]}}},
ChooseDiscoveryArchiveFolder:()=>({canceled:false,archive:{files:4,directory:'/chosen-folder/discovery-archive'}}),CancelDiscoveryArchive:()=>{},
CancelDiscoveryAssetInstall:()=>{},
GetEnhancedAnalysisStatus:()=>({installed:window.__installed,dspAvailable:true,enabled:true,mertEnabled:true,mertAvailable:window.__installed,searchableTracks:0,recommendedManifestUrl:'https://models.example/mert/manifest.json',recommendedDownloadBytes:213882011}),
InstallRecommendedMERT:()=>{window.__installed=true},
GetMetadataBundleInfo:()=>({musicBrainzConfigured:true,musicBrainzInstalled:window.__installed}),
InstallMusicBrainzBundle:()=>{window.__installed=true},
GetModelStatus:()=>({backend:'rules',ready:true}),GetLlamaRuntime:()=>({available:false,builds:[]}),
GetInstalledModels:()=>[],GetModelRecommendations:()=>({models:[],hardware:{gpuAvailable:false}}),
CompleteOnboarding:()=>{},
};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{window.__calls.push(k);const p=Promise.resolve().then(()=>{if(!o[k])throw Error('Unexpected API '+k);return o[k](...args)});p.cancel=()=>{};return p;}});`;
const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {FirstRunWizard} from '/src/screens/FirstRunWizard.tsx';import {DiscoveryDataCard} from '/src/components/DiscoveryDataCard.tsx';import '/src/design/tokens.css';ReactDOM.createRoot(document.getElementById('root')).render(location.search.includes('discovery-settings')?React.createElement('div',{className:'mx-auto max-w-[720px] p-4'},React.createElement(DiscoveryDataCard)):React.createElement(FirstRunWizard,{onDone:()=>window.__done++}));`;
const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1000, height: 760 } });
  page.on("pageerror", error => errors.push(error.message));
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: entry }));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: fixture }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: "export const Events={On:()=>()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;" }));
  for (const scenario of ["ready", "repair", "partial", "mert", "discovery", "discovery-settings"]) {
    await page.goto(`http://127.0.0.1:9245/?scenario=${scenario}`);
    if (scenario === "discovery-settings") {
      await page.getByText(/Source: Local pack override/).waitFor();
      await page.getByRole("button", { name: "Use hosted discovery data" }).click();
      await page.getByText(/Source: Hosted music collection/).waitFor();
      await page.getByRole("button", { name: "Choose local .paipack" }).click();
      await page.getByText("8 recordings ready · local-fixture").waitFor();
      await page.getByRole("button", { name: "Save manifest and parts for offline use" }).click();
      await page.getByText("Saved 4 files (manifest and archive parts) to /chosen-folder/discovery-archive").waitFor();
      await page.getByText(/Source: Local pack override/).waitFor();
    } else if (scenario === "discovery") {
      await page.getByRole("heading", { name: "Music discovery data" }).waitFor();
      assert.equal(await page.getByRole("button", { name: "Continue" }).isDisabled(), true);
      assert.equal(await page.getByRole("button", { name: "Get started" }).count(), 0);
      await page.getByRole("button", { name: "Check release size and updates" }).click();
      await page.getByText("Release download: 1.20 GB · fixture-v1.").waitFor();
      await page.getByText(/Scanning your own files with playlist-indexer is optional/).waitFor();
    } else if (scenario === "mert") {
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
    if (scenario === "discovery-settings") {
      const calls=await page.evaluate(()=>window.__calls);
      for(const method of ['InstallDiscoveryAsset','ChooseDiscoveryPack','ChooseDiscoveryArchiveFolder'])assert.equal(calls.filter(call=>call===method).length,1);
      continue;
    }
    if (scenario === "partial") await page.getByRole("button", { name: "Download offline music data" }).click();
    if (scenario === "discovery") {
      await page.getByRole("button", { name: "Download or resume discovery data" }).click();
      await page.getByText("420 recordings ready · fixture-v1").waitFor();
      assert.equal(await page.getByRole("button", { name: "Continue" }).isEnabled(), true);
      await page.getByRole("button", { name: "Continue" }).click();
    }
    if (scenario === "mert") await page.getByRole("button", { name: "Continue" }).click();
    const finish = page.getByRole("button", { name: "Start using Playlist AI" });
    await finish.waitFor();
    await finish.focus();
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => window.__done === 1);
    const calls = await page.evaluate(() => window.__calls);
    assert.ok(!calls.includes("SetPreviewProvider"), "ready or unselected steps must not run");
    assert.equal(calls.filter(call => call === "InstallRecommendedMERT").length, scenario === "mert" ? 1 : 0);
    assert.equal(calls.filter(call => call === "InstallDiscoveryAsset").length, scenario === "discovery" ? 1 : 0);
    assert.equal(calls.filter(call => call === "CompleteOnboarding").length, 1);
  }
  assert.deepEqual(errors, []);
  console.log("PASS: required ready/repair/partial/MERT/discovery setup; dark/light 390/1000; release size; optional scanning; keyboard finish; no redundant asset work");
} finally { await browser.close(); }
