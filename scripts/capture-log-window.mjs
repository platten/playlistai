// Render fixture-based log viewer checks against Vite on port 9245.
// Usage: node scripts/capture-log-window.mjs <playwright-module> <chromium> [output]
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || "/tmp/playlist-ai-log-window";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({ viewport: { width: 960, height: 640 } });
  const errors = [];
  page.on("pageerror", e => errors.push(e.message));
  await page.route("**/src/lib/api.ts", route => route.fulfill({
    contentType: "application/javascript",
    body: `
      export const FeedbackScope = {}; export const FeedbackType = {};
      export const API = {
        GetLogs: async after => {
          if (window.__fail) throw new Error("Fixture connection interrupted");
          return Array.from({length: 150}, (_,i) => ({id:i+1,level:["INFO","WARN","ERROR"][i%3],text:"Record " + (i+1) + " — application diagnostic details"})).filter(e=>e.id>after);
        },
        CloseLogWindow: async () => { window.__closed = true; }
      };
    `
  }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({
    contentType: "application/javascript",
    body: "export const Events={On:()=>()=>{}}; export const Clipboard={}; export const Call={}; export const CancellablePromise=Promise; export const System={IsMac:()=>false};"
  }));
  await page.goto("http://127.0.0.1:9245/?window=logs");
  await page.getByText("150 retained entries", { exact: false }).waitFor();
  const region = page.getByRole("region", { name: "Application log entries" });
  if (!await region.evaluate(e=>e.scrollHeight>e.clientHeight && e.scrollTop>0)) throw Error("Logs do not scroll/follow");
  await region.evaluate(e=>{e.scrollTop=0;e.dispatchEvent(new Event("scroll"));});
  await page.getByText("Automatic scrolling paused", { exact: false }).waitFor();
  for (const theme of ["light", "dark"]) {
    await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
    const colors = await page.locator("span.font-semibold").evaluateAll(nodes=>new Set(nodes.map(n=>getComputedStyle(n).color)).size);
    if(colors!==3) throw Error("Severity colors missing");
    await page.screenshot({ path: output + "/" + theme + ".png" });
  }
  await page.setViewportSize({width:420,height:480});
  await page.emulateMedia({reducedMotion:"reduce"});
  await region.focus();
  await page.keyboard.press("PageDown");
  await page.screenshot({path:output+"/narrow.png"});
  await page.evaluate(()=>{window.__fail=true;});
  await page.getByRole("alert").waitFor();
  await page.screenshot({path:output+'/error-narrow.png'});
  await page.getByRole('button',{name:'Dismiss error',exact:true}).focus();
  await page.keyboard.press('Space');
  await page.waitForTimeout(2200);
  if(await page.getByRole('alert').count()) throw Error('Polling resurrected dismissed error');
  await page.evaluate(()=>{window.__fail=false;});
  await page.waitForTimeout(1200);
  await page.evaluate(()=>{window.__fail=true;});
  await page.getByRole('alert').waitFor();
  await page.getByRole("button",{name:"Close logs"}).click();
  if(!await page.evaluate(()=>window.__closed)) throw Error("Close action not called");
  if(errors.length) throw Error(errors.join("\n"));
  console.log("PASS: scroll, follow pause, severity colors, themes, narrow layout, errors, close binding");
} finally { await browser.close(); }
