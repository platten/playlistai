// Rendered UI smoke checks with deterministic bridge fixtures, not live audio.
// Usage: node scripts/capture-recommendation-ui.mjs <playwright-module> <chromium> [output-dir]
import { mkdir, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";

const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || "/tmp/playlist-ai-recommendation-ui";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ["--no-sandbox"] });
const runtime = `
export const Events = { On(name, fn) { const handler = (e) => fn({data:e.detail}); window.addEventListener(name,handler); return () => window.removeEventListener(name,handler); } };
export const Clipboard = { SetText: async()=>{} };
export const Call = { ByID:()=>Promise.resolve(null) };
export const CancellablePromise = Promise;
`;
const api = `
export const FeedbackScope = {Session:'session',Request:'request',Global:'global'};
export const FeedbackType = {Like:'like',Dislike:'dislike'};
const cancellable = (value) => { const p=Promise.resolve(value); p.cancel=async()=>{}; return p; };
const intent = {version:8,verificationPolicy:'best_available',originalDescription:'ambient electronica',mode:'similar',essentialCriteria:[{kind:'genre',value:'ambient electronica',scope:'playlist'}],preferences:{genres:[{value:'ambient electronica',influence:'positive'}],styles:[],moods:[{value:'relaxing',influence:'positive'},{value:'sleepy',influence:'negative'}],textureDescriptions:[]},hardConstraints:[],unsupportedRequirements:[],capabilities:[],inferredAnchors:[],requiredTracks:[]};
const preview = {intent,seeds:[],requiredTracks:[],mode:'similar',count:20,creativity:.5,noise:.1,lookback:3,artistsExclude:[],resolutionIssues:[],backend:'llama',parser:{requestedBackend:'llama'},notes:''};
let active;
const emit = (data) => window.dispatchEvent(new CustomEvent('playlistai:progress',{detail:data}));
const methods = {
GetOnboarded:()=> !window.location.search.includes('wizard'), GetStatus:()=>({parserBackend:'llama'}), GetCatalogInfo:()=>({loaded:true}), ListSavedPlaylists:()=>[],
ParseIntentWithContext:()=>new Promise(resolve=>{window.__finishParse=()=>resolve(preview);}), GetPreviewProviderName:()=> 'deezer', GetModelStatus:()=>({backend:'llama',modelLabel:'Local language model'}),GetLlamaRuntime:()=>({available:true,builds:['cpu']}),GetModelCatalog:()=>[],GetModelRecommendations:()=>({models:[],hardware:{}}),GetInstalledModels:()=>[],
GetAnalysisStatus:()=>({available:false,enabled:false,model:'Music CLAP · CPU',detail:'Music CLAP is awaiting a validated model bundle. Catalog recommendations remain available.',storage:{records:0,bytes:16384},downloadBytes:0,memoryBytes:0}),
GetTasteProfile:()=>({coldStart:true,exposureCount:0}),
InspectAnalysisBundle:()=>({label:'Reviewed fixture bundle',artifacts:[{size:780000000}],memoryBytes:2000000000,license:'Apache-2.0'}),
InstallAnalysisBundle:()=>{emit({op:'analysis-model',done:320000000,total:780000000,note:'Downloading music analysis'}); return new Promise((resolve,reject)=>{window.__failDownload=()=>reject(new Error('Download interrupted. Retry to resume.'));});},
GenerateFromPromptWithContext:(prompt,context)=>new Promise((resolve)=>{active={resolve,id:context.generationId};window.__generationId=active.id;window.__advanceGeneration=()=>{emit({op:'generation',generationId:active.id,note:'Checking musical fit',done:1,total:40});emit({op:'generation',generationId:active.id,note:'Checking musical fit',checkedTrack:{id:'one',artist:'Fixture Artist',title:'Checked track'}});};}),
StopAndKeepCheckedTracks:()=>{active.resolve({playlist:{generationId:active.id,tracks:[],intent,status:{state:'partial'},outcome:{state:'partial',reasons:[{detail:'Analysis stopped with insufficient evidence.',action:'Add a fitting reference or refine your description.'}]}},request:{intent},name:'Preview fixture'});},
};
export const API = new Proxy(methods,{get(target,key){return (...args)=>{let result;try{result=(target[key]||(()=>null))(...args);}catch(e){result=Promise.reject(e);}const p=Promise.resolve(result);p.cancel=async()=>{};return p;};}});
`;

const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 850 } });
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/src/lib/api.ts", (route) => route.fulfill({ contentType: "application/javascript", body: api }));
  await page.route(/.*@wailsio_runtime\.js.*/, (route) => route.fulfill({ contentType: "application/javascript", body: runtime }));
  await page.goto("http://127.0.0.1:9245");
  const composer = page.getByRole("textbox", { name: "Describe the music you want to hear" });
  await composer.fill("Ambient electronica, relaxing but not sleepy");
  await page.getByText("The local model is reading your description…", { exact: true }).waitFor();
  await page.getByText("1s elapsed", { exact: true }).waitFor();
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => document.documentElement.dataset.theme = theme, theme);
    await page.screenshot({ path: path.join(output, `processing-${theme}.png`), fullPage: true });
  }
  await page.evaluate(() => window.__finishParse());
  await page.getByRole("heading", { name: "Your request" }).waitFor();
  if (await page.getByText("The local model is reading your description…", { exact: true }).count()) throw new Error("Processing indicator remained after parsing");
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => document.documentElement.dataset.theme = theme, theme);
    await page.screenshot({ path: path.join(output, `composer-${theme}.png`), fullPage: true });
  }
  await page.setViewportSize({ width: 560, height: 800 });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await composer.focus();
  await page.keyboard.press("Tab");
  if (await page.evaluate(() => document.activeElement === document.body)) throw new Error("Keyboard focus lost");
  await page.screenshot({ path: path.join(output, "composer-narrow.png"), fullPage: true });
  await page.getByRole("button", { name: "Generate playlist", exact: true }).click();
  await page.getByText("The local model is processing your request…", { exact: true }).waitFor();
  await page.screenshot({ path: path.join(output, "processing-generation-narrow.png"), fullPage: true });
  await page.evaluate(() => window.__advanceGeneration());
  await page.getByText("Fixture Artist — Checked track").waitFor();
	await page.evaluate(() => window.dispatchEvent(new CustomEvent("playlistai:progress", {detail:{generationId:window.__generationId,op:"generation",suggestedTrack:{id:"suggestion",artist:"Fixture Neighbor",title:"Uncertain track"}}})));
	await page.getByText("Suggested fit", {exact:true}).waitFor();
  if (await page.locator('[aria-label="Generation progress"] [role="status"]').count() < 1) throw new Error("Progress announcement missing");
  const animation = await page.locator('.pai-indeterminate').evaluate((element) => getComputedStyle(element).animationDuration);
  if (parseFloat(animation) > .01) throw new Error("Reduced-motion preference ignored");
  await page.evaluate(() => window.dispatchEvent(new CustomEvent("playlistai:progress", { detail: { generationId: "stale-generation", op: "generation", checkedTrack: { id: "stale", artist: "Wrong", title: "Stale track" } } })));
  if (await page.getByText("Wrong — Stale track").count()) throw new Error("Stale generation event rendered");
  await page.screenshot({ path: path.join(output, "checking-narrow.png"), fullPage: true });
  await page.getByRole("button", { name: "Stop and keep checked tracks" }).click();
  await page.getByText("Musical fit could not be established").waitFor();
  await page.screenshot({ path: path.join(output, "partial-result.png"), fullPage: true });
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("heading", { name: "Music analysis", exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: path.join(output, "analysis-settings.png"), fullPage: true });
  await page.getByText("Install a reviewed model bundle", { exact: true }).click();
  await page.getByRole("textbox", { name: "Bundle manifest path" }).fill("/fixture/bundle.json");
  await page.getByRole("button", { name: "Check bundle", exact: true }).click();
  await page.getByRole("button", { name: "Download bundle", exact: true }).click();
  await page.getByText("Downloading music analysis", { exact: true }).waitFor();
  await page.screenshot({ path: path.join(output, "analysis-download.png"), fullPage: true });
  await page.evaluate(() => window.__failDownload());
  await page.getByText("Error: Download interrupted. Retry to resume.", { exact: true }).waitFor();
  await page.screenshot({ path: path.join(output, "analysis-download-error.png"), fullPage: true });
  if (await page.getByRole("button", {name:"Retry download"}).count() !== 1) throw new Error("Download error lacks retry");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  await page.goto("http://127.0.0.1:9245/?wizard");
  await page.getByRole("button", {name:"Get started"}).click();
  await page.getByRole("heading", {name:"Language understanding"}).waitFor();
  await page.screenshot({path:path.join(output,"wizard-language.png"),fullPage:true});
  await page.getByRole("button", {name:"Continue",exact:true}).click();
  await page.getByRole("heading", {name:"Music analysis",exact:true}).waitFor();
  await page.screenshot({path:path.join(output,"wizard-analysis.png"),fullPage:true});
  await page.getByRole("button", {name:"Continue",exact:true}).click();
  const wizardOverflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  const report = { fixture: true, errors, narrowWindowHorizontalOverflow: overflow || wizardOverflow, screenshots: output, checked: ["light", "dark", "narrow", "reduced motion computed style", "keyboard focus", "ARIA progress announcements", "stale events", "stop", "partial", "download", "download error and retry", "wizard language and optional analysis"] };
  await writeFile(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  if (errors.length || overflow || wizardOverflow) throw new Error(JSON.stringify(report));
  console.log(JSON.stringify(report));
} finally {
  await browser.close();
}
