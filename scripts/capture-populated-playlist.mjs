// Render the actual Playlist screen with deterministic mocked data.
// node scripts/capture-populated-playlist.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
import { bridgeEnums } from "./browser-fixture-contract.mjs";

const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });

const bridge = `
${bridgeEnums}
export const API=new Proxy({}, {get:(_target,name)=>(...args)=>{const p=Promise.resolve(name==='GetPreviewURL'?{url:''}:null);p.cancel=()=>{};return p;}});`;

const entry = `
import React from '/node_modules/.vite/deps/react.js';
import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
import {PlaylistScreen} from '/src/screens/PlaylistScreen.tsx';
import {PreviewPlayerProvider} from '/src/components/index.ts';
import '/src/design/tokens.css';
const controls={audioWeight:.58,cooccurrenceWeight:.42,discovery:.16,artistDiversity:.78,transitionSmoothness:.6,totalTrackCount:10,recommendationMode:'enhanced_hybrid'};
const intent={version:5,originalDescription:'Warm late-night electronic with a steady pulse and a cinematic finish',mode:'journey',seed:'424242',trackCountExplicit:true,controls,constraints:{excludeSeedArtists:false},references:[{kind:'artist',query:'Bonobo',influence:'positive'}],requiredTracks:[],essentialCriteria:[],hardConstraints:[],preferences:{genres:[{value:'downtempo',influence:'positive',strength:'preferred'}]},journey:{waypoints:[],energyCurve:[]},capabilities:[],unsupportedRequirements:[]};
const tracks=[
['Cirrus','Bonobo','The North Borders','seed'],
['Kerala','Bonobo','Migration','nearest'],
['Lush','Four Tet','New Energy','nearest'],
['Open Eye Signal','Jon Hopkins','Immunity','nearest'],
['Glue','Bicep','Bicep','selected'],
['Atlas','Lane 8','Little by Little','selected'],
['A Walk','Tycho','Dive','interp'],
['Sun Models','ODESZA feat. Madelyn Grant','In Return','selected'],
['Emerald Rush','Jon Hopkins','Singularity','nearest'],
['Outro','M83','Hurry Up, We’re Dreaming','exploration'],
].map(([title,artist,album,kind],index)=>({id:'track-'+index,title,artist,album,kind,reason:index===0?'Starting reference':index===9?'A cinematic final lift':'Smooth energy and texture match',durationSec:214+index*7}));
const request={version:5,intent,mode:'journey',count:10,creativity:.58,noise:.16,lookback:6,seed:'424242',seedIds:['track-0'],requestId:'preview-request',generationId:'preview-generation',overrides:{},reproducibility:{id:'preview-id'}};
const assessments=[{trackId:'track-0',criteria:[{clause:{kind:'genre',text:'downtempo',coverageGroup:'fixture-mix'},state:'match',claims:[{kind:'genre',value:'Fixture genre',scope:'recording',method:'publisher_field',source:{provider:'apple',url:'https://itunes.apple.com/lookup?country=gb&id=123'}}]},{clause:{kind:'genre',text:'ambient',coverageGroup:'fixture-mix'},state:'unknown',claims:[{kind:'genre',value:'ambient',scope:'recording',method:'catalog_genre_hint',source:{provider:'catalog',url:''}}]},{clause:{kind:'instrumentation',text:'piano'},state:'unknown',detail:'Instrument prominence has not been established.',claims:[{kind:'instrumentation',value:'piano',scope:'recording',method:'quoted_statement',locator:'Fixture text describes piano on Cirrus.',source:{provider:'Fixture official source',url:'https://example.com/recordings/cirrus'}}]},{clause:{kind:'vocal',text:'instrumental'},state:'unknown',conflict:true,claims:[{kind:'vocal',value:'instrumental',scope:'recording',method:'native_vocal_hint',source:{provider:'semantic_sidecar',url:''}}]}]}];
const result={intent,tracks,assessments,generationId:'preview-generation',seed:'424242',notices:[],status:{state:'fulfilled',reasons:[]},outcome:{state:'fulfilled',reasons:[]},reproducibility:{id:'preview-id'},presentationId:'preview-presentation',mode:'journey'};
const confirmed=new URLSearchParams(location.search).get('confirmed');
if(confirmed!==null){
  result.tracks=tracks.slice(0,Number(confirmed));
  result.assessments=assessments.filter(a=>result.tracks.some(t=>t.id===a.trackId));
  result.outcome={state:'partial',reasons:[{code:'requested_genre_unconfirmed',detail:'Only tracks with corroborated support for the requested genres were retained. Unknown and conflicting genre suggestions were omitted.',action:'Add a confirmed fitting reference'}]};
  result.status=result.outcome;
}
const app=React.createElement(PreviewPlayerProvider,null,React.createElement(PlaylistScreen,{request,heading:'Late-night electronic journey',initialResult:result,sessionId:'preview-session',onBack:()=>{},onRegenerate:()=>{},onReview:()=>{}}));
ReactDOM.createRoot(document.getElementById('root')).render(app);`;

const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 850 } });
  page.on("pageerror", error => { errors.push(error.message); console.error(error.message); });
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: entry }));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: bridge }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: "export const Events={On:()=>()=>{}};export const Browser={OpenURL:url=>{window.openedSource=url;return Promise.resolve();}};export const System={IsMac:()=>false};export const Clipboard={SetText:()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;" }));
  await page.goto("http://127.0.0.1:9245");
  await page.getByText("Cirrus", { exact: true }).waitFor();
  assert.equal(await page.getByText("MERT-v1-95M · optional").count(), 0);
  assert.equal(await page.getByText(/10 tracks/).count() > 0, true);
  for (const theme of ["dark", "light"]) {
    await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
    for (const width of [1100, 390]) {
      await page.setViewportSize({ width, height: 850 });
      await page.screenshot({ path: path.join(output, `populated-playlist-${theme}-${width}.png`), fullPage: true, animations: "disabled" });
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "horizontal overflow");
      await page.getByText("Adjust playlist", { exact: true }).click();
      const details = page.getByRole("button", { name: "Track details: Bonobo — Cirrus", exact: true });
      await details.focus();
      await page.keyboard.press("Enter");
      await page.getByRole("region", { name: "Recording sources" }).waitFor();
      await page.getByText(/interpretation unverified/).waitFor();
      await page.getByText(/Each track can fit one genre/).waitFor();
      await page.getByText(/Playlist mix: downtempo/).waitFor();
      await page.getByText(/conflicting evidence/).waitFor();
      await page.getByText(/whole-recording absence unverified/).waitFor();
      await page.getByText(/library genre label; needs corroboration/).waitFor();
      await page.getByText(/Playlist mix: ambient · unverified/).waitFor();
      await page.getByText(/publisher classification/).waitFor();
      await page.getByRole("link", { name: "Apple Music" }).click();
      assert.equal(await page.evaluate(() => window.openedSource), "https://itunes.apple.com/lookup?country=gb&id=123");
      await page.getByRole("link", { name: "Fixture official source" }).click();
      assert.equal(await page.evaluate(() => window.openedSource), "https://example.com/recordings/cirrus");
      await page.screenshot({ path: path.join(output, `playlist-recording-sources-${theme}-${width}.png`), fullPage: true, animations: "disabled" });
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "recording sources overflow");
      await details.click();
      await page.getByRole("slider", { name: "Audio similarity" }).waitFor();
      await page.screenshot({ path: path.join(output, `playlist-controls-${theme}-${width}.png`), fullPage: true, animations: "disabled" });
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "expanded controls overflow");
      await page.getByText("Adjust playlist", { exact: true }).click();
    }
  }
  for (const count of [0, 2]) {
    await page.goto(`http://127.0.0.1:9245?confirmed=${count}`);
    await page.getByText(`Created ${count} of 10 requested tracks`, { exact: true }).waitFor();
    await page.getByText(/Unknown and conflicting genre suggestions were omitted/).waitFor();
    assert.equal(await page.getByRole("button", { name: "Review & export" }).isDisabled(), count === 0);
    if (count === 0) await page.getByText("No playlist", { exact: true }).waitFor();
    else await page.getByText("Cirrus", { exact: true }).waitFor();
    for (const theme of ["dark", "light"]) {
      await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
      for (const width of [1100, 390]) {
        await page.setViewportSize({ width, height: 850 });
        await page.screenshot({ path: path.join(output, `playlist-confirmed-${count}-${theme}-${width}.png`), fullPage: true, animations: "disabled" });
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "partial result overflow");
      }
    }
  }
  assert.deepEqual(errors, []);
  console.log("PASS: populated/partial/empty Playlist, export availability, recording sources, keyboard expansion and source links in both themes and widths");
} finally {
  await browser.close();
}
