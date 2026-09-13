// Render startup-update states using deterministic bridge responses.
// Usage: node scripts/capture-update-ui.mjs <playwright-module> <chromium> [output-dir]
import { mkdir, readFile } from 'node:fs/promises';
import { bridgeEnums } from './browser-fixture-contract.mjs';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import assert from 'node:assert/strict';
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || '/tmp/playlist-update-ui';
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ['--no-sandbox'] });
const notes = await readFile('docs/releases/v0.12.1.md', 'utf8') + '\n\n[Release details](https://github.com/platten/playlistai/releases/tag/v0.12.1)\n\n![No external image requests](https://example.invalid/tracker.png)';
const runtime = `
export const Events={On(name,fn){const f=e=>fn({data:e.detail});window.addEventListener(name,f);return()=>window.removeEventListener(name,f)}};
export const System={IsMac:()=>false}; export const Clipboard={SetText:async()=>{}};
export const Browser={OpenURL:async url=>{window.__openedNoteURL=url;}};
export const Call={ByID:async()=>null};export const CancellablePromise=Promise;
`;
const api = `
${bridgeEnums}
const mode=new URLSearchParams(location.search).get('update');
const releaseNotes=${JSON.stringify(notes)};
const methods={
GetOnboarded:()=>true,GetStatus:()=>({parserBackend:'rules'}),ListSavedPlaylists:()=>[],GetCatalogInfo:()=>({loaded:true}),
CheckForUpdate:()=>mode==='offline'?Promise.reject(new Error('offline')):({current:'0.12.0',version:'v0.12.1',available:true,canInstall:mode!=='readonly',reason:mode==='readonly'?'The application folder is read-only. Install the update from the release page.':'',notes:releaseNotes,size:83892728,usesInstaller:mode==='windows',notice:''}),
InstallUpdate:()=>new Promise((resolve,reject)=>{window.__updateResolve=resolve;window.__updateReject=reject;window.dispatchEvent(new CustomEvent('playlistai:progress',{detail:{op:'app-update',done:20000000,total:83892728,note:'Downloading the update'}}));}),
CancelUpdate:()=>window.__updateReject(new Error('Update canceled. The current application is unchanged.')),
OpenUpdateReleasePage:()=>{window.__openedRelease=true;}
};
export const API=new Proxy(methods,{get:(target,key)=>(...args)=>{const p=Promise.resolve().then(()=>(target[key]||(()=>null))(...args));p.cancel=()=>{};return p;}});
`;
const errors=[];
try {
  const page=await browser.newPage({viewport:{width:1100,height:850}});
  await page.emulateMedia({reducedMotion:'reduce'});
  page.on('pageerror',e=>errors.push(e.message));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/,route=>route.fulfill({contentType:'application/javascript',body:api}));
  await page.route(/.*@wailsio_runtime\.js.*/,route=>route.fulfill({contentType:'application/javascript',body:runtime}));
  await page.goto('http://127.0.0.1:9245?update=new');
  const dialog=page.getByRole('dialog');
  await dialog.waitFor();
  const region=page.getByRole('region',{name:'Release notes',exact:true});
  await region.getByRole('heading',{name:'📥 Download models directly in the app'}).waitFor();
  assert.ok(await region.locator('strong').count()>0);
  assert.ok(await region.locator('li').count()>0);
  assert.equal(await region.locator('table').count(),2);
  assert.equal(await region.locator('img,script,iframe').count(),0);
  await region.getByRole('link',{name:'Release details',exact:true}).click();
  assert.equal(await page.evaluate(()=>window.__openedNoteURL),'https://github.com/platten/playlistai/releases/tag/v0.12.1');
  assert.ok(page.url().includes('127.0.0.1'));
  await region.evaluate(e=>e.scrollTop=0);
  for(const theme of ['light','dark']){
    await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    await page.screenshot({path:path.join(output,`update-${theme}.png`)});
  }
  await page.keyboard.press('Escape');
  await dialog.waitFor({state:'detached'});
  await page.goto('http://127.0.0.1:9245?update=windows');
  await dialog.waitFor();
  await page.getByText('Windows will ask you to approve', {exact:false}).waitFor();
  await page.setViewportSize({width:480,height:740});
  await page.emulateMedia({reducedMotion:'reduce'});
  for(let i=0;i<8;i++){
    await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(()=>document.querySelector('dialog').contains(document.activeElement)),true,'focus escaped the modal');
  }
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  assert.equal(await region.evaluate(e=>e.scrollWidth<=e.clientWidth),true,'notes overflow horizontally');
  await page.screenshot({path:path.join(output,'update-windows-narrow.png')});
  await page.getByRole('button',{name:'Update and restart'}).click();
  await page.getByText('20.0 / 83.9 MB').waitFor();
  await page.keyboard.press('Escape');
  assert.equal(await dialog.isVisible(),true,'dismissed an active update');
  await page.screenshot({path:path.join(output,'update-progress.png')});
  await page.getByRole('button',{name:'Cancel download'}).click();
  await page.getByRole('alert').waitFor();
  await page.screenshot({path:path.join(output,'update-canceled.png')});
  await page.getByRole('button',{name:'Retry update'}).click();
  await page.evaluate(()=>window.__updateReject(new Error('Update checksum verification failed; the current application is unchanged.')));
  await page.getByRole('alert').waitFor();
  await page.screenshot({path:path.join(output,'update-error.png')});
  await page.getByRole('button',{name:'Dismiss error'}).click();
  await page.getByRole('button',{name:'Later'}).click();
  await dialog.waitFor({state:'detached'});
  await page.goto('http://127.0.0.1:9245?update=readonly');
  await dialog.waitFor();
  assert.equal(await page.getByRole('button',{name:'Update and restart'}).count(),0);
  await page.getByRole('button',{name:'Release page'}).click();
  assert.equal(await page.evaluate(()=>window.__openedRelease),true);
  await page.screenshot({path:path.join(output,'update-readonly.png')});
  await page.goto('http://127.0.0.1:9245?update=offline');
  await page.getByRole('textbox',{name:'Your description'}).waitFor();
  assert.equal(await page.getByRole('dialog').count(),0);
  assert.deepEqual(errors,[]);
  console.log('Startup update UI passed: themes, keyboard, narrow/reduced motion, progress, cancellation, retry, errors, read-only and offline startup.');
} finally {await browser.close();}
