// Actual Settings connection component, synthetic bridge and credentials only.
// node scripts/capture-listenbrainz-settings.mjs <playwright-module> <browser> <output>
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4]; await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const fixture = `
let status={connected:false,persistent:false,removalPending:false};
window.__connections=0;window.__disconnections=0;
const methods={GetListenBrainzStatus:()=>status,ConnectListenBrainz:()=>new Promise((resolve,reject)=>{window.__connections++;window.__complete=()=>{status={connected:true,persistent:false,removalPending:false};resolve(status)};window.__cancel=()=>reject(new Error('Cancelled'))}),DisconnectListenBrainz:()=>{window.__disconnections++;return status={connected:false,persistent:false,removalPending:false}}};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>{const p=Promise.resolve(o[k](...args));p.cancel=()=>window.__cancel?.();return p;}});`;
const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {ListenBrainzConnection} from '/src/components/ListenBrainzConnection.tsx';import '/src/design/tokens.css';ReactDOM.createRoot(document.getElementById('root')).render(React.createElement('main',{style:{maxWidth:760,margin:'24px auto',padding:16}},React.createElement(ListenBrainzConnection)));`;
const errors=[];
try {
 const page=await browser.newPage({viewport:{width:1000,height:760}});
 page.on('pageerror',error=>errors.push(error.message));
 await page.route(/\/src\/main\.tsx(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:entry}));
 await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/,r=>r.fulfill({contentType:'application/javascript',body:fixture}));
 await page.goto(`http://127.0.0.1:${process.env.PLAYLISTAI_CAPTURE_PORT || '9246'}`);
 await page.getByText(/Not connected/).waitFor();
 const input=page.getByLabel('ListenBrainz user token');
 assert.equal(await input.getAttribute('type'),'password');
 assert.equal(await page.getByRole('button',{name:'Connect',exact:true}).isDisabled(),true);
 for(const theme of ['dark','light']){
  await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
  for(const width of [1000,390]){
   await page.setViewportSize({width,height:760});
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'horizontal overflow');
   await input.focus();
   assert.equal(await input.evaluate(el=>document.activeElement===el),true);
   await page.screenshot({path:path.join(output,`listenbrainz-${theme}-${width}.png`),fullPage:true,animations:'disabled'});
  }
 }
 await input.fill('synthetic-test-token');
 await input.press('Enter');
 await page.getByRole('button',{name:'Cancel',exact:true}).waitFor();
 assert.equal(await input.inputValue(),'');
 assert.equal(await input.isDisabled(),true);
 await page.getByRole('button',{name:'Cancel',exact:true}).click();
 await page.getByRole('alert').waitFor();
 await input.fill('synthetic-test-token');
 await page.getByRole('button',{name:'Connect',exact:true}).click();
 await page.evaluate(()=>window.__complete());
 await page.getByText(/Connected for this session only/).waitFor();
 for(const theme of ['dark','light']){
  await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
  await page.screenshot({path:path.join(output,`listenbrainz-connected-${theme}.png`),fullPage:true,animations:'disabled'});
 }
 await input.fill('synthetic-replacement');
 await page.getByRole('button',{name:'Replace token'}).click();
 await page.waitForFunction(()=>window.__connections===3);
 await page.evaluate(()=>window.__complete());
 await page.getByRole('button',{name:'Disconnect',exact:true}).click();
 await page.getByText(/Not connected/).waitFor();
 assert.equal(await page.evaluate(()=>window.__disconnections),1);
 assert.deepEqual(errors,[]);
 console.log('PASS: ListenBrainz Settings dark/light 390/1000, focus, password clearing, connect/replace/disconnect, disabled controls, cancellation, session status');
}finally{await browser.close()}
