// Render fixture-based log viewer checks against Vite on port 9245.
// Usage: node scripts/capture-log-window.mjs <playwright-module> <chromium> [output]
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import assert from "node:assert/strict";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || "/tmp/playlist-ai-log-window";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ["--no-sandbox"] });
const errors = [];
try {
  const page = await browser.newPage({ viewport: { width: 960, height: 640 } });
  page.on("pageerror", e => errors.push(e.message));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({
    contentType: "application/javascript",
    body: `
      export const FeedbackScope = {}; export const FeedbackType = {};
      export const RecommendationMode = {AcousticBrainzFirst:"acousticbrainz_first",CLAPFirst:"clap_first",DeejAIOnly:"deejai_only"};
      window.__debug = sessionStorage.getItem("fixture:debug") === "true";
      window.__setCalls = [];
      let nextID = 150;
      window.__records = Array.from({length: window.location.search.includes("only-info") ? 1 : 150}, (_,i) => ({id:i+1,level:["INFO","WARN","ERROR"][i%3],text:"Record " + (i+1) + " — application diagnostic details"}));
      window.__append = (level, text) => window.__records.push({id:++nextID, level, text});
      window.__setDebug = enabled => {
        window.__debug = enabled;
        sessionStorage.setItem("fixture:debug", String(enabled));
        if (enabled) {
          window.__append("DEBUG", "Private debug fixture");
          window.__append("DEBUG+2", "Intermediate debug fixture");
        } else window.__records = window.__records.filter(e => !e.level.startsWith("DEBUG"));
      };
      if (window.__debug) window.__setDebug(true);
      const methods = {
        GetLogs: async after => {
          if (window.__fail) throw new Error("Fixture connection interrupted");
          const result = window.__records.filter(e=>e.id>after);
          if (window.__holdNextRead) {
            window.__holdNextRead = false;
            return new Promise(resolve => { window.__releaseRead = () => { resolve(result); delete window.__releaseRead; }; });
          }
          return result;
        },
        GetDebugLogging: async () => window.__debug,
        SetDebugLogging: async enabled => {
          window.__setCalls.push(enabled);
          if (window.__failSet) throw new Error("Fixture preference write failed");
          if (window.__holdSetting) await new Promise(resolve => { window.__finishSetting = resolve; });
          window.__setDebug(enabled);
        },
        CloseLogWindow: async () => { window.__closed = true; }
      };
      export const API = new Proxy(methods, { get: (target, key) => (...args) => {
        let value;
        try { value = (target[key] || (() => null))(...args); } catch (error) { value = Promise.reject(error); }
        const promise = Promise.resolve(value);
        promise.cancel = async () => {};
        return promise;
      }});
    `
  }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({
    contentType: "application/javascript",
    body: "export const Events={On:()=>()=>{}}; export const Clipboard={}; export const Call={}; export const CancellablePromise=Promise; export const System={IsMac:()=>false};"
  }));
  await page.goto("http://127.0.0.1:9245/?window=logs");
  await page.getByText("150 retained entries", { exact: false }).waitFor();
  const region = page.getByRole("region", { name: "Application log entries" });
  const level = page.getByRole("combobox", { name: "Minimum level" });
  assert.equal(await level.inputValue(), "INFO", "Ordinary logging is the safe default");
  assert.deepEqual(await level.locator("option").allTextContents(), ["DEBUG", "INFO", "WARN", "ERROR"]);
  await level.selectOption("WARN");
  await page.getByText("100 shown / 150 retained entries", { exact: false }).waitFor();
  await level.selectOption("ERROR");
  await page.getByText("50 shown / 150 retained entries", { exact: false }).waitFor();
  await level.selectOption("INFO");
  await page.getByText("150 shown / 150 retained entries", { exact: false }).waitFor();
  assert.deepEqual(await page.evaluate(() => window.__setCalls), [false, false, false], "Each explicit non-debug selection applies the shared opt-out");

  await page.evaluate(() => { window.__setDebug(true); window.__holdNextRead = true; });
  await page.waitForFunction(() => typeof window.__releaseRead === "function");
  assert.equal(await level.inputValue(), "INFO", "Hold the old viewer preference while Settings enables DEBUG");
  await level.selectOption("WARN");
  await page.waitForFunction(() => window.__debug === false);
  await page.evaluate(() => window.__releaseRead());
  await page.waitForTimeout(1200);
  assert.equal(await level.inputValue(), "WARN", "Explicit WARN wins over a not-yet-observed Settings opt-in");
  assert.equal(await region.getByText("Private debug fixture", {exact:true}).count(), 0);
  await level.selectOption("INFO");
  await page.getByText("150 shown / 150 retained entries", {exact:false}).waitFor();

  await page.evaluate(() => { window.__holdSetting = true; });
  await level.selectOption("DEBUG");
  await page.getByRole("status").getByText("Updating logging…").waitFor();
  assert.equal(await level.isDisabled(), true, "Do not allow overlapping preference writes");
  assert.equal(await page.evaluate(() => window.__debug), false, "Enable only after the preference is saved");
  await page.evaluate(() => { window.__holdSetting = false; window.__finishSetting(); });
  await region.getByText("Private debug fixture", { exact: true }).waitFor();
  await region.getByText("Intermediate debug fixture", { exact: true }).waitFor();
  assert.equal(await level.inputValue(), "DEBUG");
  await page.getByText("Detailed diagnostics are enabled", { exact: false }).waitFor();

  // Hold a read with the old opt-in state while the user disables DEBUG.
  await page.evaluate(() => { window.__append("DEBUG", "Stale private fixture"); window.__holdNextRead = true; });
  await page.waitForFunction(() => typeof window.__releaseRead === "function");
  await level.selectOption("INFO");
  await page.waitForFunction(() => window.__debug === false);
  await page.evaluate(() => { window.__append("INFO", "Recovered after stale read"); window.__releaseRead(); });
  await region.getByText("Recovered after stale read", { exact: true }).waitFor();
  assert.equal(await level.inputValue(), "INFO", "A stale poll must not restore DEBUG");
  assert.equal(await region.getByText(/private fixture|debug fixture/i).count(), 0, "Opt-out purges standard and intermediate debug records");

  await level.selectOption("DEBUG");
  await region.getByText("Private debug fixture", { exact: true }).waitFor();
  await page.evaluate(() => {
    window.__append("DEBUG", "Delayed external private fixture");
    window.__holdNextRead = true;
    window.__sawStaleDetail = false;
    window.__privacyObserver = new MutationObserver(() => {
      if (document.body.textContent.includes("Delayed external private fixture")) window.__sawStaleDetail = true;
    });
    window.__privacyObserver.observe(document.querySelector('[aria-label="Application log entries"]'), {childList:true,subtree:true});
  });
  await page.waitForFunction(() => typeof window.__releaseRead === "function");
  await page.evaluate(() => { window.__setDebug(false); window.__releaseRead(); });
  await page.waitForFunction(() => document.querySelector("select").value === "INFO");
  assert.equal(await page.evaluate(() => window.__sawStaleDetail), false, "A delayed read checks current Settings consent before rendering");
  await page.evaluate(() => window.__privacyObserver.disconnect());

  await page.evaluate(() => { window.__failSet = true; });
  await level.selectOption("DEBUG");
  await page.getByRole("alert").getByText("Could not change logging level", { exact: false }).waitFor();
  assert.equal(await level.inputValue(), "INFO", "Failed persistence retains the previous level");
  assert.equal(await page.evaluate(() => window.__debug), false);
  await page.getByRole("button", { name: "Dismiss error", exact: true }).click();
  await page.evaluate(() => { window.__failSet = false; window.__setDebug(true); });
  await region.getByText("Private debug fixture", { exact: true }).waitFor();
  assert.equal(await level.inputValue(), "DEBUG", "A Settings opt-in is reflected in the open window");
  await page.evaluate(() => window.__setDebug(false));
  await page.waitForFunction(() => document.querySelector("select").value === "INFO");
  assert.equal(await region.getByText("Private debug fixture", { exact: true }).count(), 0);
  await level.selectOption("DEBUG");
  await region.getByText("Private debug fixture", { exact: true }).waitFor();
  await page.reload();
  await region.getByText("Private debug fixture", { exact: true }).waitFor();
  assert.equal(await level.inputValue(), "DEBUG", "The persisted debug preference is restored on reopen");

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
  assert.equal(await region.getByText("Private debug fixture", { exact: true }).count(), 1, "Poll failures preserve displayed records");
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

  await page.evaluate(() => sessionStorage.removeItem("fixture:debug"));
  await page.goto("http://127.0.0.1:9245/?window=logs&only-info");
  await page.getByText("1 shown / 1 retained entries", { exact: false }).waitFor();
  await level.selectOption("ERROR");
  await region.getByText("No entries at ERROR or higher.", { exact: true }).waitFor();
  assert.equal(await page.locator("main").evaluate(e => e.scrollWidth <= e.clientWidth), true, "Narrow controls do not overflow");
  await page.screenshot({path:output+"/filtered-empty.png"});

  // Mount the real Settings component against the same deterministic bridge.
  await page.route(/\/src\/main\.tsx(?:\?.*)?$/, route => route.fulfill({
    contentType: "application/javascript",
    body: `
      import React from "/node_modules/.vite/deps/react.js";
      import ReactDOM from "/node_modules/.vite/deps/react-dom_client.js";
      import { SettingsScreen } from "/src/screens/SettingsScreen.tsx";
      import "/src/design/tokens.css";
      ReactDOM.createRoot(document.getElementById("root")).render(React.createElement(SettingsScreen));
    `,
  }));
  await page.goto("http://127.0.0.1:9245/?settings-fixture");
  const diagnosticCheckbox = page.getByRole("checkbox", { name: /Show detailed recommendation diagnostics/ });
  await diagnosticCheckbox.waitFor();
  assert.equal(await diagnosticCheckbox.isChecked(), false);
  await page.evaluate(() => { window.__setDebug(true); window.dispatchEvent(new Event("focus")); });
  await page.waitForFunction(() => [...document.querySelectorAll('input[type="checkbox"]')].some(el => el.checked));
  assert.equal(await diagnosticCheckbox.isChecked(), true, "Settings refreshes DEBUG on window focus");
  await diagnosticCheckbox.uncheck();
  await page.waitForFunction(() => window.__debug === false);
  assert.equal(await diagnosticCheckbox.isChecked(), false, "Settings still controls the shared preference");
  await page.evaluate(() => { window.__failSet = true; });
  await diagnosticCheckbox.click();
  await page.getByRole("alert").getByText("Fixture preference write failed", {exact:false}).waitFor();
  assert.equal(await diagnosticCheckbox.isChecked(), false, "Settings does not claim a failed opt-in succeeded");
  if(errors.length) throw Error(errors.join("\n"));
  console.log("PASS: minimum-level filters, debug opt-in/persistence, pending/failed writes, stale reads, Settings synchronization, filtered empty state, scroll/follow, themes, narrow layout, poll errors, close binding");
} catch (error) {
  console.error("Page errors:", errors);
  for (const context of browser.contexts()) {
    for (const page of context.pages()) {
      console.error(await page.locator("body").innerText());
      await page.screenshot({ path: output + "/failure.png" });
    }
  }
  throw error;
} finally { await browser.close(); }
