// Actual UI, mocked bridge: never removes user data or downloads assets.
// node scripts/capture-generate-settings.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const fixture = `
window.__calls=[];
const intent={preferences:{},journey:{waypoints:[],energyCurve:[]},references:[],essentialCriteria:[],hardConstraints:[],controls:{totalTrackCount:10}};
const methods={GetOnboarded:()=>true,GetStatus:()=>({parserBackend:'llama'}),GetCatalogInfo:()=>({loaded:true}),ListSavedPlaylists:()=>[],GetRecommendationMode:()=> 'acousticbrainz_first',GetPreviewProviderName:()=> 'deezer',GetModelCatalog:()=>[],GetInstalledModels:()=>[],GetModelRecommendations:()=>({models:[],hardware:{gpuAvailable:false}}),ParseIntentWithContext:()=>({intent,count:10,creativity:0.5,noise:0.1,lookback:3,seeds:[],requiredTracks:[],resolutionIssues:[]}),GenerateFromPromptWithContext:()=>new Promise(resolve=>window.__finish=resolve)};
export const RecommendationMode={AcousticBrainzFirst:'acousticbrainz_first',CLAPFirst:'clap_first',DeejAIOnly:'deejai_only',EnhancedHybrid:'enhanced_hybrid'};
export const FeedbackScope={};export const FeedbackType={};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{window.__calls.push([k,...args]);const p=Promise.resolve().then(()=>o[k]?.(...args)??null);p.cancel=()=>{window.__calls.push(['cancel',k])};return p;}});`;
const errors=[];
try {
  const page=await browser.newPage({viewport:{width:1000,height:760}});
  page.on('pageerror',error=>{errors.push(error.message);console.error(error.message)});
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/,route=>route.fulfill({contentType:'application/javascript',body:fixture}));
  await page.route(/.*@wailsio_runtime\.js.*/,route=>route.fulfill({contentType:'application/javascript',body:'export const Browser={OpenURL:()=>{}};export const Events={On:()=>()=>{}};export const System={IsMac:()=>false};export const Clipboard={SetText:()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;'}));
  await page.goto('http://127.0.0.1:9245');
  const count=page.getByRole('combobox',{name:'Number of tracks'});
  await count.waitFor();
  assert.equal(await count.inputValue(),'20');
  assert.deepEqual(await count.locator('option').allTextContents(),['5','10','20','40']);
  for(const theme of ['dark','light']) {
    await page.evaluate(value=>document.documentElement.dataset.theme=value,theme);
    for(const width of [1000,390]) {
      await page.setViewportSize({width,height:760});
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'horizontal overflow');
      await page.screenshot({path:path.join(output,`generate-${theme}-${width}.png`),fullPage:true,animations:'disabled'});
    }
  }
  await count.selectOption('10');
  await page.getByLabel('Your description').fill('A jazz playlist');
  await page.getByRole('button',{name:'Generate playlist'}).click();
  await page.waitForFunction(()=>typeof window.__finish==='function');
  await page.getByText('Detailed analysis can take several minutes on some devices. You can cancel below.').waitFor();
  await page.evaluate(()=>document.documentElement.dataset.theme='dark');
  for(const width of [1000,390]) {
    await page.setViewportSize({width,height:760});
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'processing horizontal overflow');
    await page.screenshot({path:path.join(output,`generate-processing-dark-${width}.png`),fullPage:true,animations:'disabled'});
  }
  await page.getByRole('button',{name:'Settings',exact:true}).click();
  await page.getByRole('button',{name:'Reset models and datasets',exact:true}).waitFor();
  for(const theme of ['dark','light']) {
    await page.evaluate(value=>document.documentElement.dataset.theme=value,theme);
    for(const width of [1000,390]) {
      await page.setViewportSize({width,height:760});
      await page.getByRole('button',{name:'Reset models and datasets',exact:true}).scrollIntoViewIfNeeded();
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'settings horizontal overflow');
      await page.screenshot({path:path.join(output,`settings-${theme}-${width}.png`),fullPage:true,animations:'disabled'});
    }
  }
  await page.getByRole('button',{name:'Generate',exact:true}).click();
  assert.equal(await page.getByLabel('Your description').inputValue(),'A jazz playlist');
  assert.equal(await count.inputValue(),'10');
  assert.ok(await page.getByRole('button',{name:'Generating…',exact:true}).isDisabled());
  assert.equal(await page.evaluate(()=>window.__calls.filter(c=>c[0]==='cancel'&&c[1]==='GenerateFromPromptWithContext').length),0);
  await page.getByRole('button',{name:'Settings',exact:true}).click();
  page.once('dialog',dialog=>dialog.dismiss());
  await page.getByRole('button',{name:'Reset models and datasets',exact:true}).click();
  assert.equal(await page.evaluate(()=>window.__calls.filter(c=>c[0]==='ResetAssets').length),0);
  page.once('dialog',dialog=>dialog.accept());
  await page.getByRole('button',{name:'Reset models and datasets',exact:true}).click();
  await page.getByRole('heading',{name:'Ready for a fresh setup'}).waitFor();
  assert.equal(await page.evaluate(()=>window.__calls.filter(c=>c[0]==='ResetAssets').length),1);
  assert.deepEqual(errors,[]);
  console.log('PASS: dark/light 390/1000 Generate; Settings; count selection; navigation during generation; reset cancel/confirm');
} finally {await browser.close();}
