// Render the real Generate screen with local catalog spelling fixtures.
// node scripts/capture-artist-spelling.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const fixture = `
window.__parse=[];window.__generated=[];window.__displayed=0;
const spellingCandidate={artist:"Christian Löffler",entityId:"loffler",representatives:[{trackId:"loffler-track"}]};
const methods={
GetCatalogInfo:()=>({loaded:true}),ListSavedPlaylists:()=>[],GetRecommendationMode:()=>"enhanced_hybrid",
ParseIntentWithContext:(prompt,context)=>{window.__parse.push({prompt,context});return {count:20,creativity:.5,noise:.1,lookback:3,intent:{preferences:{}},resolutionIssues:prompt.includes("christrian")?[{kind:"artist",query:"christrian loeffler",status:"ambiguous",spellingSuggestion:spellingCandidate,alternatives:[spellingCandidate]}]:[]}},
GenerateFromPromptResolvedWithContext:(prompt,selections,context)=>{window.__generated.push({prompt,selections,context});return {request:{},name:"Playlist",playlist:{generationId:context.generationId,tracks:[{id:"result"}]}}},
GenerateFromPromptWithContext:(prompt,context)=>methods.GenerateFromPromptResolvedWithContext(prompt,[],context),
};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{const p=Promise.resolve().then(()=>o[k](...args));p.cancel=()=>{};return p;}});`;
const runtime = `export const Events={On:()=>()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;`;
const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {GenerateScreen} from '/src/screens/GenerateScreen.tsx';import {PreviewPlayerProvider} from '/src/components/index.ts';import '/src/design/tokens.css';ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(PreviewPlayerProvider,null,React.createElement(GenerateScreen,{sessionId:'fixture',parserBackend:'llama',onGenerated:()=>window.__displayed++,onNeedSetup:()=>{}})));`;
const errors=[];
try {
  const page=await browser.newPage({viewport:{width:1000,height:760}});
  page.on('pageerror',e=>{errors.push(e.message);console.error(e.message);});
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:entry}));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:fixture}));
  await page.route(/.*@wailsio_runtime\.js.*/,r=>r.fulfill({contentType:'application/javascript',body:runtime}));
  await page.goto('http://127.0.0.1:9245');
  const composer=page.getByRole('textbox',{name:'Your description'});
  const original='Relaxing electronic like christrian loeffler, 20 tracks';
  await composer.fill(original);
  await page.getByRole('button',{name:'Generate playlist'}).click();
  const dialog=page.getByRole('dialog',{name:'Did you mean Christian Löffler?'});
  await dialog.waitFor();
  assert.equal(await page.evaluate(()=>window.__generated.length),0);
  for (const theme of ['dark','light']) {
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    for (const width of [1000,390]) {
      await page.setViewportSize({width,height:760});
      await page.screenshot({path:path.join(output,`artist-spelling-${theme}-${width}.png`),fullPage:true});
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'horizontal overflow');
    }
  }
  const use=dialog.getByRole('button',{name:'Use Christian Löffler'});
  const cancel=dialog.getByRole('button',{name:'Cancel'});
  await use.focus();
  await page.keyboard.press('Shift+Tab');
  assert.equal(await cancel.evaluate(el=>el===document.activeElement),true,'backward focus containment');
  await page.keyboard.press('Tab');
  assert.equal(await use.evaluate(el=>el===document.activeElement),true,'forward focus containment');
  await page.keyboard.press('Escape');
  await dialog.waitFor({state:'detached'});
  await page.waitForFunction(()=>document.activeElement?.id==='music-description');
  assert.equal(await page.evaluate(()=>window.__generated.length),0);
  await page.getByRole('button',{name:'Generate playlist'}).click();
  await dialog.getByRole('button',{name:'Use Christian Löffler'}).click();
  await page.waitForFunction(()=>window.__displayed===1);
  assert.deepEqual(await page.evaluate(()=>window.__generated[0].selections),[{kind:'artist',query:'christrian loeffler',trackId:'loffler-track'}]);
  assert.equal(await composer.inputValue(),original);
  assert.deepEqual(await page.evaluate(()=>window.__generated[0].context),await page.evaluate(()=>window.__parse[1].context));
  await page.getByRole('button',{name:'Generate playlist'}).click();
  await page.waitForFunction(()=>window.__displayed===2);
  assert.equal(await dialog.count(),0);
  await composer.fill(original+', no vocals');
  await page.getByRole('button',{name:'Generate playlist'}).click();
  await dialog.getByRole('button',{name:'Keep “christrian loeffler”'}).click();
  await page.waitForFunction(()=>window.__displayed===3);
  assert.deepEqual(await page.evaluate(()=>window.__generated[2].selections),[{kind:'artist',query:'christrian loeffler',trackId:'',rejectSpelling:true}]);
  await composer.fill('Relaxing electronic like Christian Löffler, 20 tracks');
  await page.getByRole('button',{name:'Generate playlist'}).click();
  await page.waitForFunction(()=>window.__displayed===4);
  assert.deepEqual(await page.evaluate(()=>window.__generated[3].selections),[]);
  assert.equal(await dialog.count(),0);
  assert.deepEqual(errors,[]);
  console.log('Artist spelling UI passed: accept/keep/cancel, unchanged retry, edited reset, original generation context, exact artist bypass, keyboard focus and Escape, dark/light narrow/wide.');
} finally { await browser.close(); }
