// Rendered UI smoke checks with deterministic bridge fixtures, not live audio.
// Usage: node scripts/capture-recommendation-ui.mjs <playwright-module> <chromium> [output-dir]
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";

const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || "/tmp/playlist-ai-recommendation-ui";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, ignoreDefaultArgs: ["--hide-scrollbars"], args: ["--no-sandbox", "--disable-features=OverlayScrollbar"] });
const runtime = `
export const Events = { On(name, fn) { const handler = (e) => fn({data:e.detail}); window.addEventListener(name,handler); return () => window.removeEventListener(name,handler); } };
export const Clipboard = { SetText: async()=>{} };
export const Call = { ByID:()=>Promise.resolve(null) };
export const CancellablePromise = Promise;
export const System = { IsMac:()=>new URLSearchParams(window.location.search).get('platform')==='darwin' };
`;
const api = `
export const FeedbackScope = {Session:'session',Request:'request',Global:'global'};
export const FeedbackType = {Like:'like',Dislike:'dislike'};
const cancellable = (value) => { const p=Promise.resolve(value); p.cancel=async()=>{}; return p; };
const intent = {version:8,verificationPolicy:'best_available',originalDescription:'ambient electronica',mode:'similar',essentialCriteria:[{kind:'genre',value:'ambient electronica',scope:'playlist'}],preferences:{genres:[{value:'ambient electronica',influence:'positive'}],styles:[],moods:[{value:'relaxing',influence:'positive'},{value:'sleepy',influence:'negative'}],textureDescriptions:[]},hardConstraints:[],unsupportedRequirements:[],capabilities:[],inferredAnchors:[],requiredTracks:[]};
const preview = {intent,seeds:[],requiredTracks:[],mode:'similar',count:20,creativity:.5,noise:.1,lookback:3,artistsExclude:[],resolutionIssues:[],backend:'llama',parser:{requestedBackend:'llama'},notes:''};
let active;
const tracks = Array.from({length:30}, (_,i)=>({id:String(i+1),artist:'Fixture Artist '+(i+1),title:'Playlist track '+(i+1),kind:'ranked',detail:'Fixture musical fit'}));
window.__buildCalls=0;
window.__generationCalls=0;
window.__parseCalls=0;
window.__metadataClears=0;
window.__discogsConfigured=false;
const emit = (data) => window.dispatchEvent(new CustomEvent('playlistai:progress',{detail:data}));
const methods = {
GetOnboarded:()=> !window.location.search.includes('wizard'), GetStatus:()=>({parserBackend:'llama'}), GetCatalogInfo:()=>({loaded:true}), ListSavedPlaylists:()=>[],
ParseIntentWithContext:()=>new Promise((resolve,reject)=>{window.__finishParse=(issues=[],instrumental=false,overrides={})=>resolve({...preview,...(instrumental?{backend:'rules',parser:{requestedBackend:'rules'},intent:{...intent,originalDescription:'Instrumental, no vocals',essentialCriteria:[],hardConstraints:[{kind:'exclude_vocals'}],preferences:{...intent.preferences,genres:[],vocalPreference:{value:'no vocals',influence:'positive'}}}}:{}),...overrides,resolutionIssues:issues});window.__failParse=()=>reject(new Error('Local model unavailable'));}), GetPreviewProviderName:()=> 'deezer', GetModelStatus:()=>({backend:'llama',modelLabel:'Local language model'}),GetLlamaRuntime:()=>window.__llamaRuntime??({available:true,builds:['cpu']}),GetModelCatalog:()=>window.__modelCatalog??[],GetModelRecommendations:()=>window.__modelRecommendations??({models:[],hardware:{}}),GetInstalledModels:()=>[],
BuildPlaylist:()=>{window.__buildCalls++;throw new Error('Unexpected duplicate build');},
DownloadModel:(id)=>{window.__downloadedModel=id;},
GetPreviewURL:(id)=>new Promise(resolve=>{window.__previewTrackId=id;window.__resolvePreview=resolve;}),
GetAnalysisStatus:()=>({available:false,enabled:false,model:'Music CLAP · CPU',detail:'Music CLAP is awaiting a validated model bundle. Catalog recommendations remain available.',storage:{records:0,bytes:16384},downloadBytes:0,memoryBytes:0}),
GetTasteProfile:()=>({coldStart:true,exposureCount:0}),
GetMetadataStatus:()=>({discogsConfigured:window.__discogsConfigured,credentialError:false}),
SetDiscogsToken:(token)=>{window.__discogsConfigured=Boolean(token);},
ClearMusicMetadataCache:()=>{window.__metadataClears++;return new Promise((resolve,reject)=>{window.__finishMetadataClear=resolve;window.__failMetadataClear=()=>reject(new Error('Metadata cache could not be cleared.'));});},
InspectAnalysisBundle:()=>({label:'Reviewed fixture bundle',artifacts:[{size:780000000}],memoryBytes:2000000000,license:'Apache-2.0'}),
InstallAnalysisBundle:()=>{emit({op:'analysis-model',done:320000000,total:780000000,note:'Downloading music analysis'}); return new Promise((resolve,reject)=>{window.__failDownload=()=>reject(new Error('Download interrupted. Retry to resume.'));});},
GenerateFromPromptWithContext:(prompt,context)=>new Promise((resolve,reject)=>{
  window.__generationCalls++;active={resolve,id:context.generationId};window.__generationId=active.id;
  window.__failGeneration=()=>reject(new Error('Music lookup timed out while resolving the requested artist.'));
  window.__finishGeneration=(state='fulfilled',empty=false,notices=[])=>resolve({playlist:{generationId:context.generationId,tracks:empty?[]:tracks,intent,notices,seed:'7',mode:'similar',status:{state},outcome:{state,reasons:state==='fulfilled'?[]:notices.length?[{criterion:'Missing Artist',detail:'The requested artist has no usable catalog seed.',action:'Add a specific track or another artist reference.'}]:[{criterion:'instrumental',detail:'Vocal evidence is unknown for available recordings.',action:'Add a known instrumental reference or relax the vocal requirement.'}]},reproducibility:{id:context.generationId}},request:{intent,seed:'7',reproducibility:{id:context.generationId}},name:'Your generated playlist'});
  window.__advanceGeneration=()=>{emit({op:'generation',generationId:active.id,note:'Checking musical fit',done:1,total:40});emit({op:'generation',generationId:active.id,note:'Checking musical fit',checkedTrack:{id:'one',artist:'Fixture Artist',title:'Checked track'}});};
}),
GenerateFromPromptResolvedWithContext:(prompt,selections,context)=>{window.__selections=selections;return methods.GenerateFromPromptWithContext(prompt,context);},
StopAndKeepCheckedTracks:()=>{active.resolve({playlist:{generationId:active.id,tracks:[],intent,status:{state:'partial'},outcome:{state:'partial',reasons:[{detail:'Analysis stopped with insufficient evidence.',action:'Add a fitting reference or refine your description.'}]}},request:{intent},name:'Preview fixture'});},
};
export const API = new Proxy(methods,{get(target,key){return (...args)=>{let result;try{result=(target[key]||(()=>null))(...args);}catch(e){result=Promise.reject(e);}const p=Promise.resolve(result);p.cancel=async()=>{};return p;};}});
`;

const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 850 } });
  await page.addInitScript(() => {
    const NativeAudio = window.Audio;
    window.Audio = function (...args) {
      const audio = new NativeAudio(...args);
      window.__previewAudio = audio;
      return audio;
    };
  });
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, (route) => route.fulfill({ contentType: "application/javascript", body: api }));
  await page.route(/.*@wailsio_runtime\.js.*/, (route) => route.fulfill({ contentType: "application/javascript", body: runtime }));
  await page.goto("http://127.0.0.1:9245");
  const composer = page.getByRole("textbox", { name: "Describe the music you want to hear" });
  const samples = JSON.parse(await readFile(new URL('../frontend/src/lib/generateSamples.json', import.meta.url), 'utf8'));
  for (const sample of samples) {
    await page.getByRole('button', {name:sample.prompt,exact:true}).click();
    assert.equal(await composer.inputValue(), sample.prompt, 'Each shared sample fills the exact generation request');
    assert.equal(await page.evaluate(() => window.__generationCalls), 0, 'Choosing a sample waits for submission');
  }
  await composer.fill("Classical 10 tracks");
  await page.waitForTimeout(350);
  assert.equal(await page.evaluate(() => typeof window.__finishParse), 'undefined', 'Counted genre typing must stay local');
  await page.getByRole('button', {name:'Surprise me',exact:true}).click();
  await page.waitForTimeout(350);
  assert.equal(await page.evaluate(() => window.__generationCalls), 0, 'Surprise only fills the description');
  assert.equal(await page.evaluate(() => typeof window.__finishParse), 'undefined', 'Surprise does not parse');
  await composer.fill("Ambient electronica, relaxing but not sleepy");
  await page.waitForTimeout(350);
  assert.equal(await page.evaluate(() => typeof window.__finishParse), 'undefined', 'Typing must not start parsing');
  assert.equal(await page.evaluate(() => window.__generationCalls), 0, 'Typing must not generate');
  await page.getByRole('button', {name:'Generate playlist',exact:true}).click();
  await page.getByText("The local model is processing your request…", { exact: true }).waitFor();
  assert.equal(await page.locator('#generate-playlist').isDisabled(), true, 'Generate is disabled throughout parsing');
  await page.getByText("1s elapsed", { exact: true }).waitFor();
  for (const theme of ["light", "dark"]) {
    await page.evaluate((theme) => document.documentElement.dataset.theme = theme, theme);
    await page.screenshot({ path: path.join(output, `processing-${theme}.png`), fullPage: true });
  }
  await page.evaluate(() => window.__finishParse());
  await page.getByRole("heading", { name: "Your request" }).waitFor();
  assert.equal(await page.locator('#generate-playlist').isDisabled(), true, 'Generate stays disabled during the build');
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
  const dismiss = page.getByRole('button', {name:'Dismiss request message'});
  await dismiss.focus();
  await page.keyboard.press('Enter');
  assert.equal(await page.getByRole('alert').count(), 0, 'Dismiss removes the banner');
  assert.equal(await composer.evaluate(el=>document.activeElement===el), true, 'Dismiss returns focus to composer');

  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse());
  await page.waitForFunction(()=>window.__generationCalls===2);
  await page.evaluate(()=>window.__failGeneration());
  await page.getByRole('heading',{name:'Playlist generation failed'}).waitFor();
  await page.getByRole('alert').getByText(/Music lookup timed out/).waitFor();
  assert.ok((await page.getByRole('alert').boundingBox()).y < (await composer.boundingBox()).y, 'Failure is above the composer');
  await page.screenshot({path:path.join(output,'generation-error.png'),fullPage:true});

  await composer.fill('Instrumental with no vocals');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse());
  await page.waitForFunction(()=>window.__generationCalls===3);
  await page.evaluate(()=>window.__finishGeneration('needs_clarification',true));
  await page.getByRole('heading',{name:'Refine your request'}).waitFor();
  await page.getByRole('alert').getByText(/Vocal evidence is unknown.*Add a known instrumental reference/).waitFor();
  await page.screenshot({path:path.join(output,'clarification.png'),fullPage:true});
  await composer.fill('Instrumental, no vocals');
  const instrumentalCalls=await page.evaluate(()=>window.__generationCalls);
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse([],true));
  await page.waitForFunction(n=>window.__generationCalls===n+1,instrumentalCalls);
  assert.equal(await page.evaluate(()=>window.__generationCalls),instrumentalCalls+1,'Instrumental rules prompt must allow online seed discovery');
  await page.evaluate(()=>window.__finishGeneration('fulfilled',false,[{code:'vocal_preview_screening',detail:'CLAP screened every selected preview for vocals. Unheard parts remain unassessed.'}]));
  await page.getByRole('button',{name:'Playlist',exact:true}).waitFor();
  await page.getByText('CLAP screened every selected preview for vocals.',{exact:false}).waitFor();
  await page.screenshot({path:path.join(output,'instrumental-playlist.png'),fullPage:true});
  await page.getByRole('button',{name:'Generate',exact:true}).click();
  await composer.fill('A journey from ambient to energetic electronic');
  const journeyCalls=await page.evaluate(()=>window.__generationCalls);
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse([],false,{backend:'rules',parser:{requestedBackend:'rules'},mode:'journey',count:6,intent:{version:8,mode:'journey',references:[],essentialCriteria:[{kind:'style',value:'ambient',scope:'journey_start'},{kind:'style',value:'electronic',scope:'journey_end'}],preferences:{styles:[],genres:[],moods:[{value:'energetic',influence:'positive'}]},hardConstraints:[],journey:{energyTrajectory:[{position:0,energy:.5},{position:1,energy:.8}]}}}));
  await page.getByText('Essential: ambient (start), electronic (end)',{exact:true}).waitFor();
  await page.getByText('Requested energy: build toward the end',{exact:true}).waitFor();
  assert.equal(await page.getByText('Catalog-only mode requires a seed artist or track from the catalog.',{exact:false}).count(),0,'Genre journeys must not display a mandatory artist-seed instruction');
  for(const theme of ['light','dark']) {
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    await page.screenshot({path:path.join(output,'genre-journey-'+theme+'.png'),fullPage:true});
  }
  assert.equal(await page.evaluate(()=>window.__generationCalls),journeyCalls+1,'Genre journey must not demand an artist seed');
  await page.evaluate(()=>window.dispatchEvent(new CustomEvent('playlistai:progress',{detail:{op:'generation',generationId:window.__generationId,note:'Finding recordings for each journey stage',done:0,total:0}})));
  await page.getByText('Finding recordings for each journey stage',{exact:true}).waitFor();
  await page.evaluate(()=>window.__finishGeneration());
  await page.waitForFunction(()=>document.querySelector('[aria-current="page"]')?.textContent==='Playlist');
  await page.getByRole('button',{name:'Generate',exact:true}).click();
  await composer.fill('Something like an ambiguous artist');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse([{kind:'artist',query:'Ambiguous Artist',status:'ambiguous',inferred:false,alternatives:[{entityId:'artist-a',artist:'Artist A',representatives:[{trackId:'a'}]},{entityId:'artist-b',artist:'Artist B',representatives:[{trackId:'b'}]}]}]));
  const select=page.getByRole('combobox',{name:'Choose the intended artist for “Ambiguous Artist”'});
  await select.waitFor();
  const before=await page.evaluate(()=>window.__generationCalls);
  await dismiss.click();
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await select.waitFor();
  assert.equal(await page.evaluate(()=>window.__generationCalls),before,'Dismiss does not bypass required clarification');
  await select.selectOption('b');
  await page.waitForFunction(()=>document.activeElement?.id==='generate-playlist');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse());
  await page.waitForFunction(()=>window.__selections?.length>0);
  assert.equal(await page.evaluate(()=>window.__selections[0].trackId),'b');
  await page.evaluate(()=>window.__finishGeneration());
  await page.getByRole('heading',{name:'Your generated playlist'}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Playlist',exact:true}).getAttribute('aria-current'),'page');
  assert.equal(await page.getByRole('button',{name:/^Play preview:/}).count(),30,'Every song has a preview control');

  await page.getByText('Adjust playlist',{exact:true}).click();
  const settings = ['Audio similarity','Discovery','Artist diversity','Playlist-context similarity','Transition smoothness'];
  for (const theme of ['light','dark']) {
    await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    await page.setViewportSize({width:390,height:800});
    for (const label of settings) {
      const help = page.getByRole('button',{name:'About '+label,exact:true});
      await help.scrollIntoViewIfNeeded();
      await help.hover();
      const tooltip = page.getByRole('tooltip');
      await tooltip.waitFor();
      const description = await tooltip.innerText();
      assert.ok(description.includes('Higher values'),label+' explains the setting direction');
      const box = await tooltip.boundingBox();
      assert.ok(box.x >= 0 && box.x+box.width <= 390 && box.y >= 0 && box.y+box.height <= 800,label+' help stays inside the window');
      await tooltip.hover();
      await page.waitForTimeout(200);
      assert.equal(await tooltip.isVisible(),true,'Help remains available while hovering the popup');
      if(label==='Discovery') await page.screenshot({path:path.join(output,'setting-help-'+theme+'.png'),fullPage:true});
      await page.keyboard.press('Escape');
      await tooltip.waitFor({state:'detached'});
      await help.focus();
      await tooltip.waitFor();
      await page.keyboard.press('Escape');
      await tooltip.waitFor({state:'detached'});
      await page.keyboard.press('Tab');
      const slider = page.getByRole('slider',{name:label,exact:true});
      assert.equal(await slider.evaluate(el=>document.activeElement===el),true,'Keyboard reaches the slider after its help');
      assert.equal(await slider.evaluate(el=>document.getElementById(el.getAttribute('aria-describedby'))?.textContent),description,'Slider exposes help to screen readers');
    }
  }
  await page.getByText('Adjust playlist',{exact:true}).click();
  await page.setViewportSize({width:560,height:800});

  // Exercise actual browser audio decoding with a silent WAV fixture.
  const wave=Buffer.alloc(44+16000*30);
  wave.write('RIFF');wave.writeUInt32LE(wave.length-8,4);wave.write('WAVEfmt ',8);wave.writeUInt32LE(16,16);wave.writeUInt16LE(1,20);wave.writeUInt16LE(1,22);wave.writeUInt32LE(8000,24);wave.writeUInt32LE(16000,28);wave.writeUInt16LE(2,32);wave.writeUInt16LE(16,34);wave.write('data',36);wave.writeUInt32LE(wave.length-44,40);
  const url='data:audio/wav;base64,'+wave.toString('base64');
  // Reproduce a webview interrupting a play promise while the current source
  // is still starting. A "play" event alone does not mean audio is audible.
  await page.evaluate(()=>{
    window.__previewFalseErrors=[];
    window.__previewErrorObserver=new MutationObserver(()=>{
      if (/Playback failed|The preview could not be played/.test(document.body.innerText)) window.__previewFalseErrors.push(document.body.innerText);
    });
    window.__previewErrorObserver.observe(document.body,{subtree:true,childList:true,characterData:true});
    const audio=window.__previewAudio;
    const original=audio.play.bind(audio);
    audio.play=()=>{
      audio.play=original;
      Object.defineProperty(audio,'paused',{configurable:true,get:()=>false});
      audio.dispatchEvent(new Event('play'));
      setTimeout(()=>{delete audio.paused;void original();},2000);
      return Promise.reject(new DOMException('Previous playback was interrupted','AbortError'));
    };
  });
  await page.getByRole('button',{name:'Play preview: Fixture Artist 1 — Playlist track 1',exact:true}).click();
  await page.getByRole('button',{name:'Loading preview: Fixture Artist 1 — Playlist track 1',exact:true}).waitFor();
  await page.evaluate(url=>window.__resolvePreview({available:true,url}),url);
  await page.waitForTimeout(500);
  assert.equal(await page.getByRole('button',{name:'Loading preview: Fixture Artist 1 — Playlist track 1',exact:true}).count(),1,'Starting audio stays loading until the playing event');
  await page.screenshot({path:path.join(output,'preview-buffering.png'),fullPage:true});
  await page.getByRole('button',{name:'Pause preview: Fixture Artist 1 — Playlist track 1',exact:true}).click();
  assert.deepEqual(await page.evaluate(()=>window.__previewFalseErrors),[],'No false error flashes before delayed playback starts');
  await page.evaluate(()=>window.__previewErrorObserver.disconnect());
  await page.getByRole('button',{name:'Play preview: Fixture Artist 1 — Playlist track 1',exact:true}).click();
  await page.getByRole('button',{name:'Pause preview: Fixture Artist 1 — Playlist track 1',exact:true}).waitFor();
  await page.getByRole('button',{name:'Play preview: Fixture Artist 2 — Playlist track 2',exact:true}).click();
  await page.evaluate(()=>window.__resolvePreview({available:false}));
  await page.getByRole('alert').getByText(/No preview is available/).waitFor();
  await page.getByRole('button',{name:'Retry preview: Fixture Artist 2 — Playlist track 2',exact:true}).click();
  await page.getByRole('button',{name:'Loading preview: Fixture Artist 2 — Playlist track 2',exact:true}).waitFor();
  await page.evaluate(()=>{
    const audio=window.__previewAudio;
    const original=audio.play.bind(audio);
    audio.play=()=>{audio.play=original;return Promise.reject(new DOMException('Unsupported fixture','NotSupportedError'));};
  });
  await page.evaluate(url=>window.__resolvePreview({available:true,url}),url);
  await page.getByRole('alert').getByText(/The preview could not be played/).waitFor();
  await page.screenshot({path:path.join(output,'preview-real-error.png'),fullPage:true});
  await page.getByRole('button',{name:'Dismiss error',exact:true}).click();
  assert.equal(await page.getByText(/The preview could not be played/).count(),0,'Dismiss clears row and player errors');
  await page.getByRole('button',{name:'Play preview: Fixture Artist 2 — Playlist track 2',exact:true}).click();
  await page.evaluate(()=>window.__resolvePreview({available:false}));
  await page.getByRole('alert').getByText(/No preview is available/).waitFor();
  await page.getByRole('button',{name:'Retry preview: Fixture Artist 2 — Playlist track 2',exact:true}).click();
  await page.evaluate(url=>window.__resolvePreview({available:true,url}),url);
  await page.getByRole('button',{name:'Pause preview: Fixture Artist 2 — Playlist track 2',exact:true}).waitFor();
  assert.equal(await page.getByText(/The preview could not be played/).count(),0,'Successful playback clears the previous error');
  await page.getByRole('button',{name:'Pause preview: Fixture Artist 2 — Playlist track 2',exact:true}).click();
  await page.evaluate(()=>{
    const audio=window.__previewAudio;
    const original=audio.play.bind(audio);
    audio.play=()=>{
      audio.play=original;
      void original();
      return new Promise((_,reject)=>{window.__rejectOldPlayback=reject;});
    };
  });
  await page.getByRole('button',{name:'Play preview: Fixture Artist 2 — Playlist track 2',exact:true}).click();
  await page.getByRole('button',{name:'Pause preview: Fixture Artist 2 — Playlist track 2',exact:true}).waitFor();
  await page.evaluate(()=>window.__rejectOldPlayback(new Error('Late playback rejection')));
  await page.waitForTimeout(100);
  assert.equal(await page.getByRole('button',{name:'Pause preview: Fixture Artist 2 — Playlist track 2',exact:true}).count(),1,'Late promise rejection cannot replace confirmed playback with an error');
  assert.equal(await page.getByText(/The preview could not be played/).count(),0);
  await page.waitForFunction(()=>Number.isFinite(window.__previewAudio.duration));
  await page.evaluate(()=>window.__previewAudio.currentTime=window.__previewAudio.duration);
  await page.getByRole('button',{name:'Play preview: Fixture Artist 2 — Playlist track 2',exact:true}).click();
  await page.getByRole('button',{name:'Loading preview: Fixture Artist 2 — Playlist track 2',exact:true}).waitFor();
  await page.evaluate(url=>window.__resolvePreview({available:true,url}),url);
  await page.getByRole('button',{name:'Pause preview: Fixture Artist 2 — Playlist track 2',exact:true}).waitFor();
  for(const theme of ['light','dark']) {
    await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    await page.mouse.move(20,20);
    await page.screenshot({path:path.join(output,'playlist-'+theme+'.png'),fullPage:true});
    const scroll=await page.locator('main').evaluate(el=>({right:el.getBoundingClientRect().right,width:window.innerWidth,overflow:el.scrollHeight>el.clientHeight,track:getComputedStyle(el,'::-webkit-scrollbar-track').backgroundColor,thumb:getComputedStyle(el,'::-webkit-scrollbar-thumb').backgroundColor,corner:getComputedStyle(el,'::-webkit-scrollbar-corner').backgroundColor,scheme:getComputedStyle(el).colorScheme}));
    assert.equal(scroll.right,scroll.width,'Scrollbar container reaches right window edge');
    assert.equal(scroll.overflow,true);
    assert.equal(scroll.track,theme==='dark'?'rgb(15, 15, 18)':'rgb(247, 247, 249)','Scrollbar track follows the theme');
    assert.equal(scroll.corner,scroll.track,'Scrollbar corner follows the track');
    assert.equal(scroll.thumb,theme==='dark'?'rgb(99, 99, 111)':'rgb(154, 154, 166)','Scrollbar thumb follows the theme');
    assert.equal(scroll.scheme,theme);
  }
  await page.setViewportSize({width:390,height:720});
  await page.screenshot({path:path.join(output,'playlist-narrow.png'),fullPage:true});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  await page.setViewportSize({width:1100,height:850});
  await page.screenshot({path:path.join(output,'playlist-wide.png'),fullPage:true});
  assert.equal(await page.evaluate(()=>window.__buildCalls),0,'Opening generated result must not rebuild');
  await page.getByRole('button',{name:'Generate',exact:true}).click();
  await composer.fill('Ambient electronic');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse());
  await page.waitForFunction(()=>window.__generationId);
  await page.evaluate(()=>window.__finishGeneration('partial'));
  await page.getByRole('heading',{name:'Your generated playlist'}).waitFor();
  await page.getByText('A verified partial playlist was generated',{exact:true}).waitFor();
  await page.getByRole('button',{name:'Generate',exact:true}).click();
  await composer.fill('A new musical description');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__failParse());
  await page.getByRole('alert').getByText(/Local model unavailable/).waitFor();
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse());
  await page.evaluate(()=>window.__staleFinish=window.__finishGeneration);
  await page.getByRole('button',{name:'Cancel',exact:true}).click();
  await composer.fill('A newer musical description');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse());
  await page.evaluate(()=>window.__staleFinish());
  await page.getByRole('button',{name:'Generating…',exact:true}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Generate',exact:true}).getAttribute('aria-current'),'page','Cancelled result cannot navigate over newer work');
  await page.evaluate(()=>window.__finishGeneration());
  await page.getByRole('heading',{name:'Your generated playlist'}).waitFor();
  await page.setViewportSize({width:560,height:800});
  await page.getByRole('button',{name:'Generate',exact:true}).click();
  await composer.fill('music like Missing Artist');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse([{kind:'artist',query:'Missing Artist',influence:'positive',status:'unresolved',inferred:false,alternatives:[]}]));
  assert.equal(await page.locator('#generate-playlist').isDisabled(),true,'Missing artist recovery remains part of submitted processing');
  await page.evaluate(()=>window.dispatchEvent(new CustomEvent('playlistai:progress',{detail:{op:'generation',generationId:window.__generationId,note:'Artist Missing Artist is absent from the local catalog. Checking popular tracks online.'}})));
  await page.getByText('Artist Missing Artist is absent from the local catalog. Checking popular tracks online.',{exact:true}).waitFor();
  await page.evaluate(()=>window.__finishGeneration('fulfilled',false,[{code:'music_lookup_0',detail:'Artist Missing Artist was not found under that name in the local catalog.'},{code:'music_lookup_1',detail:'Found the artist online. Using Fixture Artist — Playlist track 1 as the seed after checking 3 recordings.'}]));
  await page.getByRole('heading',{name:'Your generated playlist'}).waitFor();
  await page.getByText(/Using Fixture Artist — Playlist track 1 as the seed/).waitFor();
  await page.screenshot({path:path.join(output,'missing-artist-recovered.png'),fullPage:true});
  await page.getByRole('button',{name:'Generate',exact:true}).click();
  await composer.fill('music like Missing Artist');
  await page.getByRole('button',{name:'Generate playlist',exact:true}).click();
  await page.evaluate(()=>window.__finishParse());
  await page.evaluate(()=>window.__finishGeneration('needs_clarification',true,[{code:'music_lookup_0',detail:'Found Missing Artist online, but none of the 100 checked recordings has a verified catalog match. Try a specific track or another artist.'}]));
  await page.getByRole('alert').getByText(/none of the 100 checked recordings/).waitFor();
  await page.screenshot({path:path.join(output,'missing-artist-no-seed.png'),fullPage:true});
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const metadata = page.getByRole('region', {name:'Music metadata'});
  await metadata.getByRole('heading',{name:'Music metadata',exact:true}).scrollIntoViewIfNeeded();
  const tokenInput=metadata.getByLabel('Discogs personal API token');
  assert.equal(await tokenInput.getAttribute('type'),'password','Token input is masked');
  await tokenInput.fill('fixture-token');
  await metadata.getByRole('button',{name:'Save token',exact:true}).click();
  await metadata.getByText('Token saved. Fallback is enabled.',{exact:false}).waitFor();
  assert.equal(await tokenInput.inputValue(),'','Saved token is not left in the input');
  page.once('dialog',dialog=>dialog.dismiss());
  await metadata.getByRole('button',{name:'Clear metadata cache',exact:true}).click();
  assert.equal(await page.evaluate(()=>window.__metadataClears),0,'Cancel leaves the cache untouched');
  page.once('dialog',dialog=>dialog.accept());
  await metadata.getByRole('button',{name:'Clear metadata cache',exact:true}).click();
  await page.waitForFunction(()=>typeof window.__finishMetadataClear==='function');
  assert.equal(await metadata.getByRole('button',{name:'Clearing…',exact:true}).isDisabled(),true,'Clear stays disabled in flight');
  await page.evaluate(()=>window.__finishMetadataClear());
  await metadata.getByRole('status').getByText('Metadata cache cleared.',{exact:false}).waitFor();
  assert.equal(await page.evaluate(()=>window.__discogsConfigured),true,'Clearing metadata keeps the token');
  page.once('dialog',dialog=>dialog.accept());
  await metadata.getByRole('button',{name:'Clear metadata cache',exact:true}).click();
  await page.waitForFunction(()=>window.__metadataClears===2);
  await page.evaluate(()=>window.__failMetadataClear());
  await metadata.getByText('Metadata cache could not be cleared.',{exact:false}).waitFor();
  await metadata.getByRole('button',{name:'Remove token',exact:true}).click();
  await metadata.getByRole('status').getByText('Discogs token removed.',{exact:false}).waitFor();
  assert.equal(await page.evaluate(()=>window.__discogsConfigured),false,'Removing the token disables fallback');
  await page.screenshot({path:path.join(output,'metadata-settings.png'),fullPage:true});
  await page.getByRole("heading", { name: "Music analysis", exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: path.join(output, "analysis-settings.png"), fullPage: true });
  // Settings receives hardware-specific badges; static tier labels must not reappear.
  for (const fixture of ['gpu', 'cpu', 'none-fit']) {
    await page.evaluate(fixture => {
      window.__llamaRuntime = {available:true,builds:fixture==='cpu'?['cpu']:['gpu']};
      window.__modelCatalog = [
        {id:'large', label:'Large model', sizeApprox:22e9, params:'35B', quant:'Q4_K_M', ramGb:24, bestForVramGb:[24,32], recommended:false},
        {id:'medium', label:'Medium model', sizeApprox:5e9, params:'9B', quant:'Q4_K_M', ramGb:8, recommended:fixture==='gpu'},
        {id:'small', label:'Smallest model', sizeApprox:1.9e9, params:'3B', quant:'Q4_K_M', ramGb:4, recommended:fixture==='cpu'},
      ];
      document.documentElement.dataset.theme = fixture==='gpu'?'dark':'light';
    }, fixture);
    await page.getByRole('button',{name:'Generate',exact:true}).click();
    await page.getByRole('button',{name:'Settings',exact:true}).click();
    await page.getByText('Large model',{exact:true}).waitFor();
    assert.equal(await page.getByText('recommended',{exact:true}).count(),fixture==='none-fit'?0:1);
    assert.equal(await page.getByText(/recommended for .* GB GPU/).count(),0);
    if(fixture!=='none-fit') assert.ok((await page.getByText('recommended',{exact:true}).locator('..').textContent()).includes(fixture==='gpu'?'Medium model':'Smallest model'),'Badge belongs to the hardware-selected model');
    await page.screenshot({path:path.join(output,'settings-models-'+fixture+'.png'),fullPage:true});
  }
  await page.getByText("Use a custom CLAP model bundle", { exact: true }).click();
  await page.getByRole("textbox", { name: "Bundle manifest path" }).fill("/fixture/bundle.json");
  await page.getByRole("button", { name: "Check bundle", exact: true }).click();
  await page.getByRole("button", { name: "Download and validate custom bundle", exact: true }).click();
  await page.getByText("Downloading music analysis", { exact: true }).waitFor();
  await page.screenshot({ path: path.join(output, "analysis-download.png"), fullPage: true });
  await page.evaluate(() => window.__failDownload());
  await page.getByText("Error: Download interrupted. Retry to resume.", { exact: true }).waitFor();
  await page.screenshot({ path: path.join(output, "analysis-download-error.png"), fullPage: true });
  if (await page.getByRole("button", {name:"Retry custom download"}).count() !== 1) throw new Error("Download error lacks retry");
  await page.getByRole('button',{name:'Dismiss error',exact:true}).click();
  assert.equal(await page.getByRole('alert').count(),0,'Settings download error dismisses');
  await page.getByRole('button',{name:'Download and validate custom bundle',exact:true}).click();
  await page.evaluate(()=>window.__failDownload());
  await page.getByRole('alert').getByText('Error: Download interrupted. Retry to resume.',{exact:true}).waitFor();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  for (const fixture of ['cpu','gpu-installed','metal','smallest-only','none-fit']) {
    await page.setViewportSize({width:fixture==='cpu'?390:560,height:800});
    await page.goto("http://127.0.0.1:9245/?wizard");
    await page.evaluate(fixture=>{
      window.__llamaRuntime={available:true,builds:fixture==='cpu'?['cpu']:['gpu']};
      const smallest={id:'qwen2.5-3b-instruct-q4km',label:'Qwen2.5 3B Instruct (Q4_K_M)',params:'3B',sizeApprox:1929903264,ramGb:4,recommended:fixture==='smallest-only'||fixture==='cpu'||fixture==='gpu-installed'};
      const larger={id:'qwen3.5-9b-q4km',label:'Qwen3.5 9B (Q4_K_M)',params:'9B',sizeApprox:5680522464,ramGb:8,recommended:true};
      const models=fixture==='metal'?[larger,smallest]:[smallest];
      const hardware=fixture==='metal'?{gpuAvailable:true,gpuName:'Apple M4 (Metal)',vramBytes:8*2**30,vramFreeBytes:7*2**30,fitBytes:7*2**30,reserveBytes:1*2**30}:fixture==='smallest-only'||fixture==='none-fit'?{gpuAvailable:true,gpuName:'Fixture GPU',vramBytes:4*2**30,vramFreeBytes:(fixture==='none-fit'?1:3)*2**30,fitBytes:(fixture==='none-fit'?1:3)*2**30,reserveBytes:1*2**30}:{gpuAvailable:false};
      window.__modelRecommendations={models,hardware};
      document.documentElement.dataset.theme=fixture==='metal'?'dark':'light';
    },fixture);
    await page.getByRole("button", {name:"Get started"}).click();
    await page.getByRole("heading", {name:"Language understanding"}).waitFor();
    await page.getByText(/llama.cpp is installed/).waitFor();
    assert.equal(await page.getByText(/no usable GPU/).count(),0,'An inconclusive probe is not proof that a GPU is absent');
    assert.equal(await page.getByText(/CPU mode recommends only the smallest model/).count(),fixture==='cpu'?1:0,'Installed GPU builds do not show the CPU fallback notice');
    if(fixture==='metal') await page.getByText(/Apple M4 \(Metal\)/).waitFor();
    const smallest=page.getByRole('group',{name:'Qwen2.5 3B Instruct (Q4_K_M)',exact:true});
    await smallest.waitFor();
    assert.equal(await smallest.getByText('recommended',{exact:true}).count(),['cpu','gpu-installed','smallest-only'].includes(fixture)?1:0,'The smallest download is recommended only when it is the fitting choice');
    assert.equal(await page.getByText('recommended',{exact:true}).count(),fixture==='none-fit'?0:1,'There is at most one recommended model');
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'Model choices fit narrow windows');
    await page.screenshot({path:path.join(output,'wizard-language-'+fixture+'.png'),fullPage:true});
    await smallest.getByRole('button',{name:'Download & use',exact:true}).click();
    await page.waitForFunction(()=>window.__downloadedModel==='qwen2.5-3b-instruct-q4km');
  }
  await page.getByRole("button", {name:"Continue",exact:true}).click();
  await page.getByRole("heading", {name:"Music analysis",exact:true}).waitFor();
  await page.screenshot({path:path.join(output,"wizard-analysis.png"),fullPage:true});
  await page.getByRole("button", {name:"Continue",exact:true}).click();
  const wizardOverflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
  await page.setViewportSize({width:1040,height:800});
  for (const platform of ['darwin','windows','linux']) {
    await page.goto('http://127.0.0.1:9245/?platform='+platform);
    await page.getByRole('textbox',{name:'Describe the music you want to hear'}).waitFor();
    const header=page.locator('header');
    const brand=header.getByText('Playlist AI',{exact:true});
    const icon=header.locator('svg').first();
    const left=(await icon.boundingBox()).x;
    assert.equal(left,platform==='darwin'?96:20,'Branding reserves native controls space only on macOS');
    assert.ok((await brand.boundingBox()).x>left,'Title follows the app icon');
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    if(platform==='darwin') {
      for(const theme of ['light','dark']) {
        await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
        await header.screenshot({path:path.join(output,'mac-titlebar-'+theme+'.png')});
      }
    }
  }
  const report = { fixture: true, errors, narrowWindowHorizontalOverflow: overflow || wizardOverflow, screenshots: output, checked: ["light", "dark", "narrow", "reduced motion computed style", "keyboard focus", "ARIA progress announcements", "stale events", "stop", "partial", "dismissible detailed errors and clarification", "reference selection", "successful and partial playlist navigation without rebuild", "every-track preview loading, play, pause, unavailable and retry", "themed scrollbar at window edge", "download", "download error and retry", "wizard language and optional analysis", "Settings GPU/CPU/no-fit recommendation badges"] };
  await writeFile(path.join(output, "report.json"), JSON.stringify(report, null, 2));
  if (errors.length || overflow || wizardOverflow) throw new Error(JSON.stringify(report));
  console.log(JSON.stringify(report));
} finally {
  await browser.close();
}
