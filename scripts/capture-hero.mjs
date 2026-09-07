// Renders the real Generate screen (deterministic bridge fixtures, no live
// audio) and saves the site hero screenshot to site/app-screenshot.png.
// Usage: node scripts/capture-hero.mjs <playwright-module> <chromium>
// Requires the frontend Vite dev server running on port 9245 (`cd frontend && npx vite`).
import { pathToFileURL } from "node:url";
import path from "node:path";

const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = path.resolve("site/app-screenshot.png");
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
const intent = {version:8,verificationPolicy:'best_available',originalDescription:'ambient electronica, relaxing but not sleepy',mode:'similar',essentialCriteria:[],preferences:{genres:[{value:'ambient electronica',influence:'positive'}],styles:[],moods:[{value:'relaxing',influence:'positive'},{value:'sleepy',influence:'negative'}],textureDescriptions:[]},hardConstraints:[],unsupportedRequirements:[],capabilities:[],inferredAnchors:[],requiredTracks:[]};
const preview = {intent,seeds:[],requiredTracks:[],mode:'similar',count:25,creativity:.5,noise:.1,lookback:3,artistsExclude:[],resolutionIssues:[],backend:'llama',parser:{requestedBackend:'llama'},notes:''};
const methods = {
GetOnboarded:()=> true, GetStatus:()=>({parserBackend:'llama'}), GetCatalogInfo:()=>({loaded:true}), ListSavedPlaylists:()=>[],
ParseIntentWithContext:()=>new Promise(resolve=>setTimeout(()=>resolve(preview),50)), GetPreviewProviderName:()=> 'deezer', GetModelStatus:()=>({backend:'llama',modelLabel:'Local language model'}),GetLlamaRuntime:()=>({available:true,builds:['cpu']}),GetModelCatalog:()=>[],GetModelRecommendations:()=>({models:[],hardware:{}}),GetInstalledModels:()=>[],
GetAnalysisStatus:()=>({available:false,enabled:false,model:'Music CLAP · CPU',detail:'',storage:{records:0,bytes:0},downloadBytes:0,memoryBytes:0}),
GetTasteProfile:()=>({coldStart:true,exposureCount:0}),
};
export const API = new Proxy(methods,{get(target,key){return (...args)=>{let result;try{result=(target[key]||(()=>null))(...args);}catch(e){result=Promise.reject(e);}const p=Promise.resolve(result);p.cancel=async()=>{};return p;};}});
`;

try {
  const page = await browser.newPage({ viewport: { width: 1220, height: 900 }, deviceScaleFactor: 2 });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/src/lib/api.ts", (route) => route.fulfill({ contentType: "application/javascript", body: api }));
  await page.route(/.*@wailsio_runtime\.js.*/, (route) => route.fulfill({ contentType: "application/javascript", body: runtime }));
  await page.goto("http://127.0.0.1:9245");
  await page.evaluate(() => document.documentElement.setAttribute("data-theme", "dark"));
  const composer = page.getByRole("textbox", { name: "Describe the music you want to hear" });
  await composer.fill("Ambient electronica, relaxing but not sleepy");
  await page.getByRole("heading", { name: "Your request" }).waitFor();
  await page.waitForTimeout(150);
  if (errors.length) throw new Error("Page errors: " + errors.join("; "));
  const bottom = await page.locator("details", { hasText: "Interpretation details" }).evaluate((el) => el.getBoundingClientRect().bottom);
  await page.screenshot({ path: output, clip: { x: 0, y: 0, width: 1220, height: Math.ceil(bottom) + 28 } });
  console.log("Saved", output, "— run pngquant --quality=70-92 --speed 1 --strip --ext .png --force on it to keep it small.");
} finally {
  await browser.close();
}
