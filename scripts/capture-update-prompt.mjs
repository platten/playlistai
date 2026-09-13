// Verify the real update prompt using local fixtures; no updater is invoked.
// node scripts/capture-update-prompt.mjs <playwright-module> <browser> <output> [before]
import { mkdir } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
import assert from 'node:assert/strict';
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4];
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true });
const notes = ['## Recommendations', '- Better matches for the sound and mood you request.', '- Artist spelling suggestions preserve your choice.', '', '## Reliability', '- Faster startup and clearer progress during music analysis.', '', ...Array.from({ length: 14 }, (_, i) => `- Improvement ${i + 1}: preserve playlist intent, exclusions and saved history.`), '', '<img src=x onerror="window.__unsafe=true">', 'https://github.com/platten/playlistai/releases/' + 'long-release-reference-'.repeat(12)].join('\n');
const fixture = `
const mode=new URLSearchParams(location.search).get('update');
window.__installs=0;window.__openedRelease=0;
const methods={
CheckForUpdate:()=>({current:'0.9.1',version:'v0.9.2',available:mode!=='notice',canInstall:mode!=='manual'&&mode!=='notice',reason:mode==='manual'?'The application folder is read-only. Install the update from the release page.':'',size:83892728,usesInstaller:true,notice:mode==='notice'?'Previous install failed; your application was retained.':'',notes:mode==='missing'?'   ':mode==='notice'?'':${JSON.stringify(notes)}}),
InstallUpdate:()=>{window.__installs++;return new Promise((_resolve,reject)=>{window.__rejectUpdate=reject;window.dispatchEvent(new CustomEvent('playlistai:progress',{detail:{op:'app-update',done:20000000,total:83892728,note:'Downloading the update'}}));});},
CancelUpdate:()=>window.__rejectUpdate(new Error('Update canceled.')),
OpenUpdateReleasePage:()=>{window.__openedRelease++;}
};
export const API=new Proxy(methods,{get:(o,k)=>(...args)=>Promise.resolve().then(()=>o[k](...args))});`;
const runtime = `export const Events={On(name,fn){const f=e=>fn({data:e.detail});window.addEventListener(name,f);return()=>window.removeEventListener(name,f)}};`;
const entry = `import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {UpdatePrompt} from '/src/components/UpdatePrompt.tsx';import '/src/design/tokens.css';ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(UpdatePrompt));`;
const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 1000, height: 760 } });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  page.on('pageerror', error => errors.push(error.message));
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({ contentType: 'application/javascript', body: entry }));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: 'application/javascript', body: fixture }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: 'application/javascript', body: runtime }));
  await page.goto('http://127.0.0.1:9245?update=available');
  const dialog = page.getByRole('dialog');
  await dialog.waitFor();
  if (process.argv[5] === 'before') {
    await page.screenshot({ path: path.join(output, 'update-prompt-before.png') });
  } else {
    const region = page.getByRole('region', { name: 'Release notes' });
    await region.waitFor();
    assert.equal(await region.isVisible(), true, 'notes should be visible without expansion');
    assert.equal(await dialog.locator('details').count(), 0);
    assert.equal(await region.locator('img,script').count(), 0, 'notes interpreted as HTML');
    for (const theme of ['dark', 'light']) {
      await page.evaluate(value => document.documentElement.dataset.theme = value, theme);
      for (const width of [1000, 390]) {
        await page.setViewportSize({ width, height: 760 });
        await dialog.evaluate(element => { element.scrollTop = 0; });
        await region.evaluate(element => { element.scrollTop = 0; });
        assert.ok(await region.evaluate(element => element.scrollHeight > element.clientHeight && element.clientHeight > 100), 'notes scroll bounds');
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'page horizontal overflow');
        assert.ok(await region.evaluate(element => element.scrollWidth <= element.clientWidth), 'notes horizontal overflow');
        assert.ok(await page.getByRole('button', { name: 'Update and restart' }).evaluate(element => {
          const rect = element.getBoundingClientRect();
          return rect.top >= 0 && rect.bottom <= innerHeight;
        }), 'install action should stay visible');
        await page.screenshot({ path: path.join(output, `update-prompt-${theme}-${width}.png`) });
      }
    }
    await region.focus();
    await page.keyboard.press('End');
    await page.waitForFunction(() => document.querySelector('[aria-label="Release notes"]').scrollTop > 0);
    const dismiss = page.getByRole('button', { name: 'Dismiss update' });
    const install = page.getByRole('button', { name: 'Update and restart' });
    await dismiss.focus();
    await page.keyboard.press('Shift+Tab');
    assert.equal(await install.evaluate(element => element === document.activeElement), true, 'backwards focus containment');
    await page.keyboard.press('Tab');
    assert.equal(await dismiss.evaluate(element => element === document.activeElement), true, 'forwards focus containment');
    await page.getByRole('button', { name: 'Release page' }).click();
    await page.waitForFunction(() => window.__openedRelease === 1);
    assert.equal(await page.evaluate(() => window.__installs), 0);
    await install.click();
    await page.getByRole('button', { name: 'Cancel download' }).waitFor();
    const progress = page.getByRole('progressbar');
    assert.ok(await progress.evaluate(element => {
      const rect = element.getBoundingClientRect();
      return rect.top >= 0 && rect.bottom <= innerHeight;
    }), 'download progress should stay visible');
    await page.screenshot({ path: path.join(output, 'update-prompt-progress-390.png') });
    await page.keyboard.press('Escape');
    assert.equal(await dialog.isVisible(), true);
    assert.equal(await region.isVisible(), true);
    await page.getByRole('button', { name: 'Cancel download' }).click();
    await page.getByRole('button', { name: 'Retry update' }).waitFor();
    assert.ok(await page.getByRole('alert').evaluate(element => {
      const rect = element.getBoundingClientRect();
      return rect.top >= 0 && rect.bottom <= innerHeight;
    }), 'update error should stay visible');
    await page.screenshot({ path: path.join(output, 'update-prompt-error-390.png') });
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'detached' });
    await page.goto('http://127.0.0.1:9245?update=missing');
    await page.getByText('Release notes are unavailable here. Open the release page on GitHub for details.').waitFor();
    await page.getByRole('button', { name: 'Release page' }).click();
    await page.waitForFunction(() => window.__openedRelease === 1);
    await page.goto('http://127.0.0.1:9245?update=manual');
    await region.waitFor();
    assert.equal(await page.getByRole('button', { name: 'Update and restart' }).count(), 0);
    await page.goto('http://127.0.0.1:9245?update=notice');
    await page.getByRole('heading', { name: 'Your previous update needs attention' }).waitFor();
    assert.equal(await page.getByRole('heading', { name: 'What’s new' }).count(), 0);
    assert.deepEqual(errors, []);
    console.log('Update notes UI passed: visible safe text, bounded keyboard scroll, dark/light390/1000, focus containment, release page, download/cancel/retry, missing notes and recovery.');
  }
} finally { await browser.close(); }
