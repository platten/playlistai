// Actual components with deterministic bridge fixtures; no models or previews.
// node scripts/capture-enhanced-audio.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4]; await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const fixture = `
let enabled=true,installed=false;
window.__installs=[];window.__analyses=[];
const status=()=>({enabled,installed,dspAvailable:true,mertAvailable:installed,limit:24,revision:installed?'12af15fef9d0ac838c3f475bfbbf26d2060dd4f5':'',downloadBytes:installed?395000000:0,dspStorage:{records:4,bytes:1024},mertStorage:{records:2,bytes:4096},detail:'Preview measurements describe the analyzed interval.'});
const methods={GetEnhancedAnalysisStatus:status,SetEnhancedAnalysisEnabled:v=>{enabled=v},InstallMERT:p=>{window.__installs.push(p);return new Promise((resolve,reject)=>{window.__finish=()=>{installed=true;resolve()};window.__cancel=()=>reject(new Error('Model pack cancelled'));})},RemoveMERT:()=>{installed=false},ClearEnhancedAnalysis:()=>{},AnalyzeEnhancedTracks:(ids,liked)=>{window.__analyses.push({ids,liked});return new Promise((resolve,reject)=>{window.__finish=()=>resolve({analyzed:1,unavailable:1});window.__cancel=()=>reject(new Error('Analysis cancelled'));})}};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{const p=Promise.resolve(o[k](...args));p.cancel=()=>window.__cancel?.();return p;}});
`;
const runtime = `export const Events={On:()=>()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;`;
const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {EnhancedAudioCard} from '/src/components/EnhancedAudioCard.tsx';import '/src/design/tokens.css';ReactDOM.createRoot(document.getElementById('root')).render(React.createElement('main',{style:{maxWidth:760,margin:'24px auto',padding:16}},React.createElement(EnhancedAudioCard,{trackIds:['one','two']})));`;
const errors=[];
try {
  const page=await browser.newPage({viewport:{width:1000,height:1000}});
  page.on('pageerror',e=>{errors.push(e.message);console.error(e.message)});
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:entry}));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:fixture}));
  await page.route(/.*@wailsio_runtime\.js.*/,r=>r.fulfill({contentType:'application/javascript',body:runtime}));
  await page.goto('http://127.0.0.1:9245');
  await page.getByRole('heading',{name:'Enhanced audio analysis'}).waitFor();
  await page.getByText(/Available · no model download required/).waitFor();
  for (const theme of ['dark','light']) {
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    await page.screenshot({path:path.join(output,`enhanced-${theme}.png`),fullPage:true});
    await page.setViewportSize({width:390,height:844});
    await page.screenshot({path:path.join(output,`enhanced-${theme}-narrow.png`),fullPage:true});
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'no horizontal overflow');
    await page.setViewportSize({width:1000,height:1000});
  }
  await page.getByLabel('MERT pack directory or manifest').fill(' https://models.example/mert/manifest.json ');
  await page.getByRole('button',{name:'Install MERT pack'}).click();
  await page.getByRole('button',{name:'Cancel model installation'}).waitFor();
  await page.screenshot({path:path.join(output,'mert-pack-progress.png'),fullPage:true});
  await page.getByRole('button',{name:'Cancel model installation'}).click();
  await page.getByRole('alert').filter({hasText:'Model pack cancelled'}).waitFor();
  await page.screenshot({path:path.join(output,'mert-pack-cancelled.png'),fullPage:true});
  await page.getByRole('button',{name:'Install MERT pack'}).click();
  await page.waitForFunction(()=>window.__installs.length===2);
  await page.evaluate(()=>window.__finish());
  await page.getByText(/Revision: 12af/).waitFor();
  assert.deepEqual(await page.evaluate(()=>window.__installs),['https://models.example/mert/manifest.json','https://models.example/mert/manifest.json']);
  await page.getByRole('button',{name:'Analyze candidates'}).click();
  await page.getByRole('button',{name:'Cancel analysis'}).click();
  await page.getByRole('alert').filter({hasText:'Analysis cancelled'}).waitFor();
  await page.getByRole('button',{name:'Analyze liked tracks'}).click();
  await page.evaluate(()=>window.__finish());
  await page.getByRole('status').filter({hasText:'1 unavailable'}).waitFor();
  assert.deepEqual(await page.evaluate(()=>window.__analyses),[{ids:['one','two'],liked:false},{ids:[],liked:true}]);
  await page.getByRole('button',{name:'Remove MERT'}).click();
  await page.getByText(/Not installed · CC-BY-NC/).waitFor();
  assert.deepEqual(errors,[]);
  console.log('Enhanced UI: dark/light, narrow/wide, install/remove, bounded analysis and cancellation passed');
} finally {await browser.close();}
