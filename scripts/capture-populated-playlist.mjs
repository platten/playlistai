// Render the actual Playlist screen with deterministic mocked data.
// node scripts/capture-populated-playlist.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";

const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });

const bridge = `
export const FeedbackScope={FeedbackScopeRequest:'request',FeedbackScopeDurable:'durable'};
export const FeedbackType={FeedbackLike:'like',FeedbackDislike:'dislike',FeedbackMoreLike:'more_like',FeedbackLessLike:'less_like',FeedbackAccepted:'accepted',FeedbackRemoved:'removed'};
export const API=new Proxy({}, {get:(_target,name)=>(...args)=>{const p=Promise.resolve(name==='GetPreviewURL'?{url:''}:null);p.cancel=()=>{};return p;}});`;

const entry = `
import React from '/node_modules/.vite/deps/react.js';
import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
import {PlaylistScreen} from '/src/screens/PlaylistScreen.tsx';
import {PreviewPlayerProvider} from '/src/components/PreviewPlayer.tsx';
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
const result={intent,tracks,assessments:[],generationId:'preview-generation',seed:'424242',notices:[],status:{state:'fulfilled',reasons:[]},outcome:{state:'fulfilled',reasons:[]},reproducibility:{id:'preview-id'},presentationId:'preview-presentation',mode:'journey'};
const app=React.createElement(PreviewPlayerProvider,null,React.createElement(PlaylistScreen,{request,heading:'Late-night electronic journey',initialResult:result,sessionId:'preview-session',onBack:()=>{},onRegenerate:()=>{},onReview:()=>{}}));
ReactDOM.createRoot(document.getElementById('root')).render(app);`;

const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 850 } });
  page.on("pageerror", error => errors.push(error.message));
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: entry }));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: bridge }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: "export const Events={On:()=>()=>{}};export const Browser={OpenURL:()=>{}};export const System={IsMac:()=>false};export const Clipboard={SetText:()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;" }));
  await page.goto("http://127.0.0.1:9245");
  await page.getByText("Cirrus", { exact: true }).waitFor();
  assert.equal(await page.getByText("MERT-v1-95M · optional").count(), 0);
  assert.equal(await page.getByText(/10 tracks/).count() > 0, true);
  await page.evaluate(() => { document.documentElement.dataset.theme = "dark"; });
  await page.screenshot({ path: path.join(output, "populated-playlist-dark.png"), fullPage: true, animations: "disabled" });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "horizontal overflow");
  assert.deepEqual(errors, []);
  console.log("PASS: populated Playlist screen rendered without the MERT setup card");
} finally {
  await browser.close();
}
