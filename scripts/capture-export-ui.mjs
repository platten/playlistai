// Deterministic browser fixtures; no metadata or export services are contacted.
// Usage: node scripts/capture-export-ui.mjs <playwright-module> <chromium> [output]
import { mkdir } from "node:fs/promises";
import { bridgeEnums } from "./browser-fixture-contract.mjs";
import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || "/tmp/playlist-ai-export-ui";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 850 } });
  page.setDefaultTimeout(15000);
  const errors = [];
  page.on("pageerror", e => { errors.push(e.message); console.error(e.message); });
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: `
    import React from '/node_modules/.vite/deps/react.js'; import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
    import App from '/src/App.tsx'; import {ReviewExport} from '/src/screens/ReviewExport.tsx';
    import {FirstRunWizard} from '/src/screens/FirstRunWizard.tsx'; import '/src/design/tokens.css';
    const mode=location.search;
    const component=mode.includes('review')?React.createElement(ReviewExport,{trackIds:['one','two'],heading:'Local mix',requestId:'request',sessionId:'session',onBack:()=>{}}):mode.includes('wizard')?React.createElement(FirstRunWizard,{onDone:()=>{}}):React.createElement(App);
    ReactDOM.createRoot(document.getElementById('root')).render(component);
  ` }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: `
    export const Events={On(){return ()=>{};}}; export const Clipboard={SetText:async()=>{}};
    export const Call={}; export const CancellablePromise=Promise;
    export const System={IsMac:()=>false};
  ` }));
  await page.route("**/src/lib/api.ts", route => route.fulfill({ contentType: "application/javascript", body: `
    ${bridgeEnums}
    const rows=[{id:'one',artist:'Local artist',title:'First track',album:'Local album'},{id:'two',artist:'Second artist',title:'Second track',album:''}];
    let failed=false;
    const methods={
      GetOnboarded:()=>true,GetStatus:()=>({parserBackend:'rules'}),GetCatalogInfo:()=>({loaded:!location.search.includes('missing')}),ListSavedPlaylists:()=>[],
      GetModelStatus:()=>({backend:'llama',modelId:'fixture'}),GetLlamaRuntime:()=>({available:true,builds:['cpu']}),GetInstalledModels:()=>[],GetModelRecommendations:()=>({models:[],hardware:{}}),
      GetAnalysisStatus:()=>({installed:false,available:false,storage:{bytes:0,records:0}}),
      GetPreviewProviderName:()=>location.search.includes('spotify')?'spotify':'off',
      SetPreviewProvider:p=>{window.__savedProvider=p;if(!failed){failed=true;throw Error('Could not save preview preference');}},
      PrepareExport:()=>{if(!failed){failed=true;throw Error('Local read failed');}return location.search.includes('empty')?[]:rows;},
      EnrichPlaylist:()=>{throw Error('Removed enrichment API called');},
      ExportCSV:(name,tracks)=>{window.__exported=tracks;return {path:'/tmp/local.csv',count:tracks.length,canceled:false};},
      OpenSoundiizHandoff:(name,tracks)=>{window.__exported=tracks;return {url:'https://soundiiz.com/import/fixture',count:tracks.length,opened:false};}
    };
    export const API=new Proxy(methods,{get(target,key){return (...args)=>{let p;try{p=Promise.resolve(target[key]?.(...args));}catch(e){p=Promise.reject(e);}p.cancel=async()=>{};return p;};}});
  ` }));
  await page.goto("http://127.0.0.1:9245/?missing");
  await page.getByRole("button", {name:"Open setup",exact:true}).waitFor();
  if(await page.getByRole("button", {name:"Catalog",exact:true}).count()) throw Error("Catalog navigation remains");
  await page.getByRole("button", {name:"Open setup",exact:true}).click();
  await page.getByRole("button", {name:"Get started",exact:true}).waitFor();

  await page.goto("http://127.0.0.1:9245/?review");
  await page.getByText("Error: Local read failed", {exact:true}).waitFor().catch(async e => { console.error(await page.locator('body').innerText()); throw e; });
  await page.getByRole('button',{name:'Dismiss error',exact:true}).focus();
  await page.keyboard.press('Enter');
  if(await page.getByRole('alert').count()) throw Error('Export load error was not dismissed');
  await page.getByRole('button',{name:'Try again',exact:true}).click();
  await page.getByRole("cell", {name:"First track Local artist"}).waitFor();
  if(await page.getByText(/ISRC|Confidence|MusicBrainz/).count()) throw Error("Removed export fields remain");
  await page.getByRole("checkbox", {name:"Include Second artist — Second track"}).uncheck();
  for(const theme of ["light","dark"]) {
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    await page.screenshot({path:output+'/export-'+theme+'.png',fullPage:true});
  }
  await page.getByRole("button", {name:"Download CSV",exact:true}).click();
  await page.getByText("Saved 1 tracks to", {exact:false}).waitFor();
  if(await page.evaluate(()=>window.__exported.length!==1 || window.__exported[0].id!=='one')) throw Error("Export selection changed");
  await page.getByRole("button", {name:"Open Soundiiz handoff",exact:true}).click();
  await page.getByText("Soundiiz import ready for 1 tracks", {exact:false}).waitFor();
  await page.setViewportSize({width:560,height:850});
  await page.screenshot({path:output+'/export-narrow.png',fullPage:true});
  if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)) throw Error("Export horizontal overflow");
  await page.goto("http://127.0.0.1:9245/?review-empty");
  await page.getByRole("button", {name:"Try again",exact:true}).click();
  await page.getByText("Nothing to export", {exact:true}).waitFor();

  for(const suffix of ['', '-spotify']) {
    await page.goto("http://127.0.0.1:9245/?wizard"+suffix);
    await page.getByRole("button", {name:"Get started",exact:true}).click();
    await page.getByRole("button", {name:"Continue",exact:true}).click();
    await page.getByRole("heading", {name:"Music analysis",exact:true}).waitFor();
    await page.getByRole("button", {name:"Continue",exact:true}).click();
    await page.getByRole("heading", {name:"Track previews",exact:true}).waitFor();
    if(await page.getByRole("button", {name:/^Off/}).count()) throw Error("Off is offered in wizard");
    const expected=suffix?'Spotify':'Deezer';
    if(await page.getByRole("button", {name:new RegExp('^'+expected)}).getAttribute('aria-pressed')!=='true') throw Error("Wrong preview selection");
    await page.getByRole("button", {name:"Continue",exact:true}).click();
    await page.getByText("Error: Could not save preview preference", {exact:true}).waitFor();
    await page.getByRole('button',{name:'Dismiss error',exact:true}).click();
    if(await page.getByRole('alert').count()) throw Error('Wizard save error was not dismissed');
    await page.getByRole("heading", {name:"Track previews",exact:true}).waitFor();
    await page.screenshot({path:output+'/preview'+suffix+'.png',fullPage:true});
    await page.getByRole("button", {name:"Continue",exact:true}).click();
    await page.getByRole("heading", {name:"You're set up",exact:true}).waitFor();
    if(await page.evaluate(()=>window.__savedProvider)!==expected.toLowerCase()) throw Error("Preview selection not saved");
  }
  if(errors.length) throw Error(errors.join('\n'));
  console.log('PASS: no catalog navigation, setup recovery, local export/retry/selection/empty states, both preview choices, off migration and save failure, themes and narrow layout');
} finally { await browser.close(); }
