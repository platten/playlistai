// Render the static site and check interactions without external requests.
// Usage: node scripts/capture-site.mjs <playwright-module> <chromium> [output]
// Add --social to regenerate site/og.png from the site's own HTML/CSS.
import { createServer } from "node:http";
import { readFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const directory = path.resolve('site');
const output = process.argv[4] || '/tmp/playlist-ai-site';
await mkdir(output, { recursive: true });
const server = createServer(async (req, res) => {
  const filename = new URL(req.url, 'http://localhost').pathname;
  const allowed = new Set(['/', '/index.html', '/styles.css', '/site.js', '/favicon.svg', '/og.png', '/app-screenshot.png']);
  if (!allowed.has(filename)) { res.writeHead(404).end(); return; }
  try {
    const target = path.join(directory, filename === '/' ? 'index.html' : filename);
    const content = await readFile(target);
    const types = { '.html': 'text/html', '.css': 'text/css', '.js': 'text/javascript', '.svg': 'image/svg+xml', '.png': 'image/png' };
    res.writeHead(200, {'Content-Type': types[path.extname(target)]}).end(content);
  } catch { res.writeHead(404).end(); }
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ['--no-sandbox'] });
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1050 } });
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.route('**/*', route => {
    if (route.request().url().startsWith(origin)) return route.continue();
    errors.push('Unexpected external request: ' + route.request().url());
    return route.abort();
  });
  await page.goto(origin);
  if (await page.locator('h1').count() !== 1) throw Error('Expected one main heading');
  await page.keyboard.press('Tab');
  if (await page.evaluate(() => document.activeElement.textContent) !== 'Skip to content') throw Error('Skip link not first keyboard target');
  if (await page.locator('.studio-screenshot[alt]').count() !== 1) throw Error('Hero screenshot missing alt text');
  const missingAnchors = await page.evaluate(() => [...document.querySelectorAll('a[href^="#"]')].filter(a => !document.getElementById(a.hash.slice(1))).map(a => a.hash));
  if (missingAnchors.length) throw Error('Missing anchor targets: '+missingAnchors.join(','));
  const downloads = await page.locator('a[href*="/releases/download/"]').evaluateAll(links => links.map(a => a.href));
  if (downloads.length !== 13 || downloads.some(url => !url.includes('/v0.7.0/'))) throw Error('Incorrect release downloads');
  if (!(await page.locator('.release-link').innerText()).includes('COMING IN VERSION 0.8.0')) throw Error('Draft release incorrectly advertised');
  if (!(await page.locator('.version-label').innerText()).includes('PUBLIC DOWNLOAD / 0.7.0')) throw Error('Public download version unclear');
  if (await page.locator('.measurement-grid article').count() !== 3) throw Error('Release measurements missing');
  await page.getByRole('link', {name:'Read the 0.8.0 release notes'}).waitFor();
  for (const width of [1440, 1024, 768, 390, 320]) {
    await page.setViewportSize({width,height:1000});
    await page.evaluate(() => { document.querySelectorAll('details').forEach(d => d.open = false); window.scrollTo(0,0); });
    if ((await page.locator('h1').innerText()).includes('musicyou')) throw Error('Headline words run together');
    const overflow = await page.evaluate(() => [...document.querySelectorAll('body *')].filter(e => e.getBoundingClientRect().right > innerWidth + 1 && getComputedStyle(e).position !== 'absolute').map(e => e.className));
    if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw Error(`Horizontal overflow at ${width}: ${overflow}`);
    await page.screenshot({path:path.join(output,`site-${width}.png`),fullPage:true});
  }
  await page.getByRole('button', {name:'Open navigation'}).click();
  await page.getByRole('link', {name:'The experience',exact:true}).waitFor();
  await page.keyboard.press('Escape');
  if (await page.getByRole('button', {name:'Open navigation'}).getAttribute('aria-expanded') !== 'false') throw Error('Escape did not close menu');
  await page.getByRole('button', {name:'Open navigation'}).click();
  await page.getByRole('link', {name:'Privacy',exact:true}).click();
  if (await page.locator('#navigation').evaluate(e => e.classList.contains('open'))) throw Error('Navigation did not close');
  await page.emulateMedia({reducedMotion:'reduce'});
  if (await page.evaluate(() => getComputedStyle(document.documentElement).scrollBehavior) !== 'auto') throw Error('Reduced motion ignored');
  await page.getByText('Does CLAP work on every build?', {exact:true}).click();
  await page.getByText('Native inference was validated', {exact:false}).waitFor();
  await page.getByText('Packages & portable downloads', {exact:true}).click();
  if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw Error('Expanded download overflow');
  await page.screenshot({path:path.join(output,'site-mobile-expanded.png'),fullPage:true});
  const noJS = await browser.newPage({javaScriptEnabled:false,viewport:{width:390,height:900}});
  await noJS.goto(origin);
  await noJS.getByRole('link', {name:'Download for Windows',exact:false}).waitFor();
  if (await noJS.locator('.experience-grid .feature').count() !== 4) throw Error('Content missing without JavaScript');
  if (await noJS.locator('.measurement-grid article').count() !== 3) throw Error('Measurements missing without JavaScript');
  await noJS.close();
  if (process.argv.includes('--social')) {
    await page.setViewportSize({width:1200,height:630});
    await page.goto(origin);
    await page.addStyleTag({content:'.site-header,.assurance,main>section:not(.hero),.site-footer,.skip-link,.hero-actions,.hero-note,.demo-caption{display:none!important}.hero{width:1080px;padding:25px 0 0;gap:50px;height:630px}.hero h1{font-size:69px;letter-spacing:-4px}.studio{padding-top:65px}.record{width:290px}.hero-copy:before{content:"Playlist AI";display:block;font-size:20px;margin-bottom:30px;color:#b4b2ff}.release-link{font-size:9px}'});
    await page.screenshot({path:path.join(directory,'og.png')});
  }
  if (errors.length) throw Error(errors.join('\n'));
  console.log('PASS: five viewports, keyboard examples and navigation, 13 versioned downloads, anchors, reduced motion, no-JS content, no external requests, no browser errors');
} finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
