// Render actual setup controls with deterministic provider fixtures.
// node scripts/capture-intent-models.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4]; await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const fixture = `
let installed=false,enabled=false,attempts=0;window.__packs=[];
const status=()=>location.search.includes('unsupported')?{installed:false,enabled:false,downloadBytes:0,unsupportedReason:'Compact intent models require macOS 14 or newer.',detail:'Compact intent models require macOS 14 or newer.'}:{installed,enabled,downloadBytes:428000000,detail:"MiniLM dictionary suggestions are experimental and optional. DistilBERT's base encoder is prepared for training; extraction stays inactive until a reviewed, calibrated task model is available."};
const methods={GetIntentAssistStatus:status,SetIntentAssistEnabled:v=>{enabled=v},InstallIntentModelPack:source=>{window.__packs.push(source);return new Promise((resolve,reject)=>{window.__finish=resolve;window.__cancel=()=>reject(new Error('Model pack cancelled'));})},InstallIntentModels:()=>{attempts++;if(attempts===1)return Promise.reject(new Error('Download interrupted'));installed=true}};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{const p=Promise.resolve().then(()=>o[k](...args));p.cancel=()=>{if(k==='InstallIntentModelPack')window.__cancel?.()};return p;}});`;
const runtime = `export const Events={On:()=>()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;`;
const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {IntentModelsCard} from '/src/components/IntentModelsCard.tsx';import '/src/design/tokens.css';ReactDOM.createRoot(document.getElementById('root')).render(React.createElement('main',{style:{maxWidth:760,margin:'24px auto',padding:16}},React.createElement(IntentModelsCard,{automatic:true})));`;
const errors=[];
try {
  const page=await browser.newPage({viewport:{width:1000,height:760}});
  page.on('pageerror',e=>errors.push(e.message));
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:entry}));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:fixture}));
  await page.route(/.*@wailsio_runtime\.js.*/,r=>r.fulfill({contentType:'application/javascript',body:runtime}));
  await page.goto('http://127.0.0.1:9245');
  await page.getByRole('alert').filter({hasText:'Download interrupted'}).waitFor();
  await page.screenshot({path:path.join(output,'intent-models-retry.png'),fullPage:true});
  await page.getByRole('button',{name:'Retry intent model download'}).click();
  await page.getByText('MiniLM and DistilBERT assets verified.').waitFor();
  const toggle=page.getByRole('checkbox');
  assert.equal(await toggle.isChecked(),false);
  await toggle.focus();await page.keyboard.press('Space');
  await page.waitForFunction(()=>document.querySelector('input[type=checkbox]').checked);
  await page.getByText('Install a compressed model pack',{exact:true}).click();
  await page.getByLabel('Model pack manifest URL or path').fill(' https://models.example/intent/manifest.json ');
  for (const theme of ['dark','light']) {
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    for (const width of [1000,390]) {
      await page.setViewportSize({width,height:760});
      await page.screenshot({path:path.join(output,`intent-models-${theme}-${width}.png`),fullPage:true});
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'horizontal overflow');
    }
  }
  await page.getByRole('button',{name:'Install model pack',exact:true}).click();
  await page.getByRole('progressbar',{name:'Preparing intent models'}).waitFor();
  await page.screenshot({path:path.join(output,'intent-pack-progress.png'),fullPage:true});
  await page.getByRole('button',{name:'Cancel model preparation'}).click();
  await page.getByRole('alert').filter({hasText:'Model pack cancelled'}).waitFor();
  await page.screenshot({path:path.join(output,'intent-pack-cancelled.png'),fullPage:true});
  await page.getByRole('button',{name:'Install model pack',exact:true}).click();
  await page.waitForFunction(()=>window.__packs.length===2);
  await page.evaluate(()=>window.__finish());
  await page.waitForFunction(()=>!document.querySelector('[aria-busy="true"]'));
  assert.deepEqual(await page.evaluate(()=>window.__packs),['https://models.example/intent/manifest.json','https://models.example/intent/manifest.json']);
  await page.goto('http://127.0.0.1:9245/?unsupported=1');
  await page.getByText('Your existing prompt parser remains available.').waitFor();
  assert.equal(await page.getByRole('button',{name:/Download intent models/}).count(),0);
  assert.equal(await page.getByRole('alert').count(),0);
  await page.screenshot({path:path.join(output,'intent-models-unsupported.png'),fullPage:true});
  assert.deepEqual(errors,[]);
  console.log('Intent setup UI passed: automatic download failure/retry, compressed manifest install/cancel/retry, installed state, keyboard toggle, dark/light and narrow/wide, unsupported host without download.');
} finally {await browser.close();}
