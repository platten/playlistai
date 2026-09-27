// Render the actual Automatic Playlist screen and exercise reversible identity.
// node scripts/capture-automatic-playlist.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
import { bridgeEnums } from "./browser-fixture-contract.mjs";

const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
const baseURL = `http://127.0.0.1:${process.env.PLAYLISTAI_CAPTURE_PORT || "9245"}`;
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const bridge = `${bridgeEnums}
export const API=new Proxy({}, {get:(_target,name)=>(...args)=>{
  let result=null;
  if(name==='GetPreviewURL') result={url:''};
  if(name==='BuildPlaylist') {
    window.builds.push(args[0]);
    result=structuredClone(window.fixtureResult);
    const chosen=args[0].artistSelections[0]?.identityId;
    if(chosen) {
      const g=result.intent.references[0].grounding;
      g.alternatives=g.candidates.filter(c=>c.id!==chosen);
      g.candidates=g.candidates.filter(c=>c.id===chosen);
      g.confirmed=true;g.decision=null;
    }
  }
  const p=Promise.resolve(result);p.cancel=()=>{};return p;
}});`;
const screenSource = await (await fetch(`${baseURL}/src/screens/PlaylistScreen.tsx`)).text();
const reactURL = screenSource.match(/from "(\/node_modules\/\.vite\/deps\/react\.js[^"]*)"/)?.[1];
assert.ok(reactURL, "Vite React module URL");
const entry = `
import React from '${reactURL}';
import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
import {PlaylistScreen} from '/src/screens/PlaylistScreen.tsx';
import {PreviewPlayerProvider} from '/src/components/index.ts';
import '/src/design/tokens.css';
const controls={audioWeight:.5,cooccurrenceWeight:.5,discovery:.4,artistDiversity:.7,transitionSmoothness:.5,totalTrackCount:10,recommendationMode:'automatic'};
const intent={version:5,originalDescription:'Music like Fela, 10 tracks',mode:'similar',seed:'9223372036854775806',trackCountExplicit:true,controls,constraints:{excludeSeedArtists:false},references:[{kind:'artist',query:'Fela',influence:'positive',grounding:{provider:'MusicBrainz',matchedSpelling:'Fela',matchType:'alias',snapshotVersion:'fixture',decision:{selectedId:'famous',method:'popularity',provisional:true},candidates:[{kind:'artist',id:'famous',name:'Fela Kuti',disambiguation:'Nigerian musician'},{kind:'artist',id:'namesake',name:'Fela',disambiguation:'another performer'}]}}],requiredTracks:[],essentialCriteria:[],hardConstraints:[],preferences:{genres:[]},journey:{waypoints:[],energyCurve:[]},capabilities:[],unsupportedRequirements:[]};
const request={version:5,intent,mode:'similar',count:10,creativity:.5,noise:.4,lookback:5.5,seed:intent.seed,seedIds:[],requestId:'fixture',overrides:{},reproducibility:{id:'fixture-id'}};
const tracks=[{id:'track-0',artist:'Fela Kuti',title:'Fixture recording with a long multilingual title — 音楽',kind:'selected',reason:'Related artist and audio evidence'}];
const fitAssessments=[{trackId:'track-0',assessment:{state:'strong',detail:'Independent metadata and audio agree.',clauses:[{clause:{kind:'genre',text:'afrobeat'},state:'strong',signals:[{detail:'Sampled audio supports this estimate.',coverage:{available:true,coveredSeconds:30}}]}]}}];
const result={intent,tracks,fitAssessments,generationId:'fixture',seed:intent.seed,notices:[],status:{state:'partial',reasons:[]},outcome:{state:'partial',reasons:[{code:'insufficient_matches',detail:'Only supported matches were retained.'}]},reproducibility:{id:'fixture-id'},presentationId:'fixture-presentation',mode:'similar'};
window.fixtureResult=result;window.builds=[];
function Fixture(){
 const [revision,setRevision]=React.useState(0);
 const save=React.useCallback(value=>{window.draft=value;},[]);
 return React.createElement(React.Fragment,null,React.createElement('button',{onClick:()=>setRevision(n=>n+1)},'Remount playlist'),React.createElement(PreviewPlayerProvider,null,React.createElement(PlaylistScreen,{key:revision,request,heading:'Automatic playlist',initialResult:result,initialDraft:revision?window.draft:undefined,sessionId:'fixture-session',onDraft:save,onBack:()=>{},onRegenerate:()=>{},onReview:()=>{}})));
}
ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(Fixture));`;
const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 850 } });
  page.on("pageerror", error => { errors.push(error.message); console.error(error.message); });
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: entry }));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: bridge }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: "export const Events={On:()=>()=>{}};export const Browser={OpenURL:()=>Promise.resolve()};export const System={IsMac:()=>false};export const Clipboard={SetText:()=>{}};export const Call={ByID:()=>Promise.resolve(null)};export const CancellablePromise=Promise;" }));
  await page.goto(baseURL);
  await page.getByText("Created 1 of 10 requested tracks", { exact: true }).waitFor();
  await page.getByText("Change artist", { exact: true }).focus();
  await page.keyboard.press("Enter");
  await page.getByRole("combobox", { name: "Artist for Fela" }).selectOption("namesake");
  await page.getByText("Your artist choice.", { exact: true }).waitFor();
  assert.equal(await page.evaluate(() => window.builds.at(-1).artistSelections[0].identityId), "namesake");
  assert.equal(await page.evaluate(() => window.builds.at(-1).overrides.seed), "9223372036854775806");
  await page.getByRole("button", { name: "Remount playlist" }).click();
  await page.getByText("Your artist choice.", { exact: true }).waitFor();
  assert.equal(await page.evaluate(() => window.draft.artistSelections[0].identityId), "namesake");
  await page.getByRole("button", { name: /Track details: Fela Kuti/ }).click();
  await page.getByText(/does not verify the whole recording/).waitFor();
  for (const theme of ["dark", "light"]) {
    await page.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
    for (const width of [1100, 390]) {
      await page.setViewportSize({ width, height: 850 });
      await page.screenshot({ path: path.join(output, `automatic-playlist-${theme}-${width}.png`), fullPage: true, animations: "disabled" });
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "horizontal overflow");
    }
  }
  assert.deepEqual(errors, []);
  console.log("PASS: Automatic artist correction, draft restoration, lossless seed, partial result and estimated evidence; dark/light at1100/390px");
} finally {
  await browser.close();
}
