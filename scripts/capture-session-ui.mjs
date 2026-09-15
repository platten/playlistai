// Real rendered navigation and saved-selection races with deferred bridge calls.
// Usage: node scripts/capture-session-ui.mjs <playwright-module> <chromium> [output]
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import assert from "node:assert/strict";
import { bridgeEnums } from "./browser-fixture-contract.mjs";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || "/tmp/playlist-ai-session-ui";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ["--no-sandbox"] });
const runtime = `
export const Events={On(){return ()=>{};}};export const System={IsMac:()=>false};
export const Clipboard={SetText:async()=>{}};export const Call={};export const CancellablePromise=Promise;
export const Browser={OpenURL:async()=>{}};
`;
const api = `
${bridgeEnums}
window.__acks=[];window.__parses=[];window.__builds=[];
window.__exports=[];
const controls={audioWeight:.5,cooccurrenceWeight:.5,discovery:.1,artistDiversity:.7,transitionSmoothness:.2,totalTrackCount:2,recommendationMode:'clap_first'};
function fixture(name,count=2){
 const intent={controls:{...controls,totalTrackCount:count},mode:'similar',count,seed:'18446744073709551615',originalDescription:name,references:[],requiredTracks:[],constraints:{excludeSeedArtists:false},preferences:{},knowledge:{}};
 const request={version:2,requestId:name,sessionId:'session',intent,count,reproducibility:{id:name}};
 const result={intent,presentationId:name,reproducibility:{id:name},outcome:{state:'fulfilled',reasons:[]},tracks:Array.from({length:count},(_,i)=>({id:name+'-'+i,title:name+' song '+(i+1),artist:'Artist '+(i+1),kind:'selected'}))};
 return {request,result};
}
const methods={
 GetOnboarded:()=>true,GetStatus:()=>({parserBackend:'llama'}),GetCatalogInfo:()=>({loaded:true}),GetRecommendationMode:()=> 'clap_first',
 ListSavedPlaylists:()=>[{id:'A',name:'Saved A',prompt:'Original A',trackCount:2},{id:'B',name:'Saved B',prompt:'Original B',trackCount:2}],
 LoadSavedPlaylist:id=>id==='A'?fixture('A'):new Promise(resolve=>{window.__finishB=()=>resolve(fixture('B'));}),
 ParseIntentWithContext:q=>{window.__parses.push(q);return {intent:fixture('Edited').request.intent,count:3,creativity:.5,noise:.1,lookback:3,seeds:[],requiredTracks:[],resolutionIssues:[]};},
 GenerateFromPromptWithContext:(q,ctx)=>{const f=fixture('Edited',3);return {request:f.request,playlist:{...f.result,generationId:ctx.generationId},name:q};},
 BuildPlaylist:request=>{window.__builds.push(request);return fixture('Adjusted',request.overrides.totalTrackCount).result;},
 AcknowledgePlaylistDisplayed:id=>{window.__acks.push(id);},
 PrepareExport:ids=>ids.map(id=>({id,title:id,artist:'Artist',album:''})),
 RecordTrackAcceptance:()=>new Promise(resolve=>{window.__acceptExport=()=>resolve();}),
 OpenSoundiizHandoff:(name,tracks)=>{window.__exports.push({name,tracks});return new Promise(resolve=>{window.__finishExport=()=>resolve({url:'https://soundiiz.com/import/fixture',count:tracks.length,opened:false});});},
 GetPreviewProviderName:()=> 'deezer',GetModelCatalog:()=>[],GetTasteProfile:()=>null,GetDebugLogging:()=>false,
};
export const API=new Proxy(methods,{get(target,key){return (...args)=>{let value;try{value=target[key]?.(...args);}catch(e){value=Promise.reject(e);}const promise=Promise.resolve(value);promise.cancel=async()=>{};return promise;};}});
`;
try {
  const page = await browser.newPage({ viewport: { width: 1000, height: 800 } });
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: api }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: runtime }));
  await page.goto("http://127.0.0.1:9245");
  await page.getByRole("radio", { name: "a past playlist" }).check();
  await page.getByRole("combobox", { name: "Previous playlist" }).selectOption("A");
  await page.getByRole("button", { name: "Generate playlist" }).waitFor({ state: "visible" });
  await page.getByRole("combobox", { name: "Previous playlist" }).selectOption("B");
  await page.getByText("Loading saved playlist…").waitFor();
  assert.equal(await page.getByRole("button", { name: "Generate playlist" }).isDisabled(), true);
  await page.getByRole("textbox", { name: "Your description" }).press("Enter");
  assert.deepEqual(await page.evaluate(() => window.__parses), []);
  for (const theme of ["dark", "light"]) {
    await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
    await page.screenshot({ path: output + `/saved-loading-${theme}.png`, fullPage: true });
  }
  await page.getByRole("combobox", { name: "Previous playlist" }).selectOption("A");
  await page.evaluate(() => window.__finishB());
  await page.getByRole("textbox", { name: "Your description" }).fill("Original A, exclude Artist 1");
  await page.getByText(/Your edited description will generate a new playlist/).waitFor();
  await page.setViewportSize({ width: 390, height: 850 });
  assert.ok((await page.getByRole("combobox", { name: "Previous playlist" }).boundingBox()).width >= 300,
    "The selected history name must remain readable in the narrow stress-test viewport");
  await page.screenshot({ path: output + "/saved-edited-narrow.png", fullPage: true });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  await page.getByRole("button", { name: "Generate playlist" }).click();
  await page.getByText("Edited song 3", { exact: true }).waitFor();
  assert.deepEqual(await page.evaluate(() => window.__parses), ["Original A, exclude Artist 1"]);
  await page.getByText("Adjust playlist", { exact: true }).click();
  await page.getByRole("button", { name: "increase Total tracks" }).click();
  await page.getByText("Adjusted song 4", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByText("Track previews", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Playlist", exact: true }).click();
  await page.getByText("Adjusted song 4", { exact: true }).waitFor();
  await page.getByRole("button", { name: "Review & export" }).click();
  await page.getByText("Adjusted-3", { exact: true }).waitFor();
  await page.getByRole("textbox").fill("Retained export title");
  await page.getByRole("checkbox").first().uncheck();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "Review & export" }).click();
  assert.equal(await page.getByRole("textbox").inputValue(), "Retained export title");
  assert.equal(await page.getByRole("checkbox").first().isChecked(), false);
  await page.getByRole("button", { name: "Open Soundiiz handoff" }).click();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Export", exact: true }).click();
  assert.equal(await page.getByRole("button", { name: "Open Soundiiz handoff" }).isDisabled(), true);
  await page.getByRole("textbox").fill("Next export title");
  await page.getByRole("checkbox").nth(1).uncheck();
  await page.evaluate(() => window.__acceptExport());
  await page.waitForFunction(() => window.__exports.length === 1);
  assert.equal(await page.evaluate(() => window.__exports[0].name), "Retained export title");
  assert.equal(await page.evaluate(() => window.__exports[0].tracks.length), 3);
  assert.equal(await page.getByRole("progressbar", { name: "Sending to Soundiiz" }).getAttribute("aria-valuetext"), "0 / 3");
  for (const theme of ["dark", "light"]) {
    await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
    await page.screenshot({ path: output + `/export-pending-${theme}-narrow.png`, fullPage: true });
  }
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
  const exportBounds = await page.getByRole("button", { name: "Open Soundiiz handoff" }).boundingBox();
  assert.ok(exportBounds.x >= 0 && exportBounds.x + exportBounds.width <= 390, "Export action must remain fully visible at narrow widths");
  assert.ok((await page.getByRole("textbox", { name: "Playlist name" }).boundingBox()).height >= 34, "Playlist name input must retain its usable height");
  await page.getByRole("button", { name: "Playlist", exact: true }).click();
  await page.evaluate(() => window.__finishExport());
  await page.getByRole("button", { name: "Review & export" }).click();
  await page.getByText("Soundiiz import ready for 3 tracks", { exact: false }).waitFor();
  assert.equal(await page.getByRole("textbox").inputValue(), "Next export title");
  await page.screenshot({ path: output + "/export-completed-after-navigation.png", fullPage: true });
  assert.deepEqual(await page.evaluate(() => window.__acks), ["Edited", "Adjusted"]);
  assert.equal(await page.evaluate(() => window.__builds.length), 1);
  assert.equal(await page.evaluate(() => window.__builds[0].overrides.seed), "18446744073709551615");
  assert.deepEqual(errors, []);
  console.log("PASS: saved-selection races, retained drafts, immutable pending export across navigation, completion while unmounted, lossless seeds, acknowledgments, themes and narrow layout");
} finally {
  await browser.close();
}
