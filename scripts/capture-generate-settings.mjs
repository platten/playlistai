// Actual UI, mocked bridge: never removes user data or downloads assets.
// node scripts/capture-generate-settings.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
const port = process.env.PLAYLISTAI_CAPTURE_PORT || '9245';
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const fixture = `
window.__calls=[];window.__setupReady=false;
let modelDevice='CUDA0';
const intent={preferences:{},journey:{waypoints:[],energyCurve:[]},references:[],essentialCriteria:[],hardConstraints:[],controls:{totalTrackCount:10}};
const methods={GetOnboarded:()=>true,GetSetupStatus:()=>({pending:!window.__setupReady,onboarded:true,needsSetup:false,pendingSteps:[],repairSteps:[]}),GetStatus:()=>({parserBackend:'llama',version:'0.14.2'}),GetCatalogInfo:()=>({loaded:true}),ListSavedPlaylists:()=>[],GetRecommendationMode:()=> 'enhanced_hybrid',GetPreviewProviderName:()=> 'deezer',GetModelCatalog:()=>[],GetInstalledModels:()=>[],GetModelRecommendations:()=>({models:[],hardware:{mode:modelDevice==='cpu'?'cpu':'gpu',gpuAvailable:modelDevice!=='cpu',gpuName:modelDevice==='CUDA0'?'NVIDIA RTX Test':'AMD Radeon Test',selectedDevice:modelDevice,devices:[{id:'CUDA0',name:'NVIDIA RTX Test',totalBytes:8e9,freeBytes:7e9,nvidia:true},{id:'Vulkan1',name:'AMD Radeon Test',totalBytes:16e9,freeBytes:12e9,nvidia:false}]}}),SetModelDevice:id=>{modelDevice=id},ParseIntentWithContext:()=>({intent,count:10,creativity:0.5,noise:0.1,lookback:3,seeds:[],requiredTracks:[],resolutionIssues:[]}),GenerateFromPromptWithContext:()=>new Promise(resolve=>window.__finish=resolve)};
export const RecommendationMode={AcousticBrainzFirst:'acousticbrainz_first',CLAPFirst:'clap_first',DeejAIOnly:'deejai_only',EnhancedHybrid:'enhanced_hybrid'};
export const FeedbackScope={};export const FeedbackType={};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{window.__calls.push([k,...args]);const p=Promise.resolve().then(()=>o[k]?.(...args)??null);p.cancel=()=>{window.__calls.push(['cancel',k])};return p;}});`;
const errors=[];
try {
  const page=await browser.newPage({viewport:{width:1000,height:760}});
  page.on('pageerror',error=>{errors.push(error.message);console.error(error.message)});
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/,route=>route.fulfill({contentType:'application/javascript',body:fixture}));
  await page.route(/.*@wailsio_runtime\.js.*/,route=>route.fulfill({contentType:'application/javascript',body:'export const Browser={OpenURL:()=>{}};export const Events={On:()=>()=>{}};export const System={IsMac:()=>false};export const Clipboard={SetText:()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;'}));
  await page.goto(`http://127.0.0.1:${port}`);
  await page.getByText(/Checking installed audio models/).waitFor();
  await page.getByLabel('Your description').fill('Ambient electronica with a gentle pulse');
  assert.equal(await page.getByRole('button',{name:'Generate playlist'}).isDisabled(),true);
  for(const theme of ['dark','light']) {
    await page.evaluate(value=>document.documentElement.dataset.theme=value,theme);
    for(const width of [1000,390]) {
      await page.setViewportSize({width,height:760});
      assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'startup horizontal overflow');
      await page.screenshot({path:path.join(output,`generate-startup-${theme}-${width}.png`),fullPage:true,animations:'disabled'});
    }
  }
  await page.evaluate(()=>window.__setupReady=true);
  await page.getByText(/Checking installed audio models/).waitFor({state:'hidden'});
  assert.equal(await page.getByLabel('Your description').inputValue(),'Ambient electronica with a gentle pulse');
  assert.equal(await page.getByRole('button',{name:'Generate playlist'}).isEnabled(),true);
  const count=page.getByRole('combobox',{name:'Number of tracks'});
  await count.waitFor();
  assert.equal(await count.inputValue(),'20');
  assert.deepEqual(await count.locator('option').allTextContents(),['5','10','20','40']);
  for(const theme of ['dark','light']) {
    await page.evaluate(value=>document.documentElement.dataset.theme=value,theme);
    const contrastFailures = await page.evaluate(() => {
      const style = getComputedStyle(document.documentElement);
      const luminance = (token) => {
        const hex = style.getPropertyValue(`--pai-${token}`).trim().replace('#', '');
        const channels = hex.match(/../g).map(part => parseInt(part, 16) / 255)
          .map(value => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
        return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
      };
      const failures = [];
      for (const foreground of ['text','muted','faint','accent','good','warn','bad']) {
        for (const background of ['bg','surface','inset']) {
          const values = [luminance(foreground), luminance(background)].sort((a,b) => b-a);
          const ratio = (values[0] + 0.05) / (values[1] + 0.05);
          if (ratio < 4.5) failures.push(`${foreground}/${background}: ${ratio.toFixed(2)}`);
        }
      }
      return failures;
    });
    assert.deepEqual(contrastFailures, [], `${theme} small-text token contrast`);
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
  assert.equal(await page.getByRole('radio',{name:/AcousticBrainz first/}).count(),0);
  assert.equal(await page.getByRole('radio',{name:/CLAP first/}).count(),0);
  assert.equal(await page.getByRole('radio',{name:/Enhanced hybrid/}).isChecked(),true);
  await page.getByText('Playlist AI 0.14.2').waitFor();
  await page.getByText('Paul Pietkiewicz').waitFor();
  await page.getByText('GPL-3.0').waitFor();
  const device=page.getByRole('combobox',{name:'Model compute device'});
  await device.waitFor();
  assert.equal(await device.inputValue(),'CUDA0');
  assert.deepEqual(await device.locator('option').allTextContents(),['NVIDIA RTX Test · 7.0 GB free · NVIDIA','AMD Radeon Test · 12.0 GB free','CPU · system memory']);
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
  console.log('PASS: startup validation and dark/light 390/1000 Generate; Settings; count selection; navigation during generation; reset cancel/confirm');
} finally {await browser.close();}
