// Vite fixture checks for wizard downloads; real native inference is tested in Go.
// Usage: node scripts/capture-clap-wizard.mjs <playwright-module> <chromium> [output]
import { mkdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.argv[2]).href);
const output = process.argv[4] || "/tmp/playlist-ai-clap-wizard-ui";
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.argv[3], headless: true, args: ["--no-sandbox"] });
try {
  const page = await browser.newPage({ viewport: { width: 1100, height: 950 } });
  const errors = [];
  page.on("pageerror", e => { errors.push(e.message); console.error(e.message); });
  await page.route("**/src/main.tsx", route => route.fulfill({ contentType: "application/javascript", body: `
    import React from '/node_modules/.vite/deps/react.js'; import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
    import {FirstRunWizard} from '/src/screens/FirstRunWizard.tsx';
    import '/src/design/tokens.css';
    ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(FirstRunWizard,{onDone:()=>{}}));
  ` }));
  await page.route(/.*@wailsio_runtime\.js.*/, route => route.fulfill({ contentType: "application/javascript", body: `
    export const Events={On(name,fn){ const handler=e=>fn({data:e.detail});window.addEventListener(name,handler);return ()=>window.removeEventListener(name,handler);}};
    export const Clipboard={}; export const Call={}; export const CancellablePromise=Promise;
  ` }));
  await page.route(/\/src\/lib\/api\.ts(?:\?.*)?$/, route => route.fulfill({ contentType: "application/javascript", body: `
    let installed=false; let attempts=0;
    export const FeedbackScope={}; export const FeedbackType={};
    const bundle={label:'CLAP Music + Speech · full precision',artifacts:[{size:793130000}],memoryBytes:2147483648,license:'Apache-2.0'};
    const emit=note=>window.dispatchEvent(new CustomEvent('playlistai:progress',{detail:{op:'analysis-model',done:123450000,total:793130000,note}}));
    const download=()=>{
      attempts++; emit('Downloading music analysis');
      let rejectCall;
      const p=new Promise((resolve,reject)=>{rejectCall=reject;window.__finish=()=>{installed=true;emit('Music analysis ready');resolve();};window.__fail=()=>reject(new Error('Download interrupted'));window.__validate=()=>emit('Checking music analysis');});
      p.cancel=()=>{rejectCall(new Error('Download stopped'));return Promise.resolve();};return p;
    };
    const methods={
      GetCatalogInfo:()=>({loaded:true}),GetModelStatus:()=>({backend:'llama',modelId:'qwen9'}),GetLlamaRuntime:()=>({available:true,builds:['cpu']}),GetInstalledModels:()=>[],
      GetModelRecommendations:()=>({models:[{id:'qwen3',label:'Qwen2.5 3B',params:'3B',sizeApprox:1929903264,ramGb:4,recommended:true,installed:true}],hardware:{gpuAvailable:false}}),
      GetRecommendedAnalysisBundle:()=>bundle,
      GetAnalysisStatus:()=>({installed,available:installed,generalFitAvailable:false,enabled:false,model:bundle.label,detail:installed?'CLAP compares previews with your description to help rank tracks and screens no-vocals requests. Similarity scores are not calibrated judgments of musical fit.':'Download a CLAP model.',storage:{bytes:0,records:0},downloadBytes:793130000,memoryBytes:2147483648}),
      InspectAnalysisBundle:()=>({...bundle,label:'Custom CLAP fixture'}),
      RemoveAnalysisModel:()=>{installed=false;},
      InstallRecommendedAnalysisBundle:download,InstallAnalysisBundle:download,
    };
    export const API=new Proxy(methods,{get(target,key){return (...args)=>{const value=target[key]?.(...args);return value instanceof Promise?value:Promise.resolve(value);};}});
  ` }));
  await page.goto("http://127.0.0.1:9245/?wizard");
  await page.getByRole("button", {name:"Get started"}).click();
  await page.getByRole("heading", {name:"Language understanding"}).waitFor();
  if(await page.getByText("recommended",{exact:true}).count() !== 1) throw Error("Wizard must have one language model recommendation");
  await page.getByRole("button",{name:"Continue",exact:true}).click();
  await page.getByRole("heading",{name:"Music analysis",exact:true}).waitFor();
  for(const theme of ["light","dark"]) {
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    await page.screenshot({path:output+"/recommended-"+theme+".png",fullPage:true});
  }
  await page.getByRole("button",{name:"Download and validate CLAP",exact:true}).click();
  await page.getByText("Downloading music analysis",{exact:true}).waitFor();
  await page.getByText("123.5 MB / 793.1 MB",{exact:true}).waitFor();
  if(await page.getByRole('progressbar',{name:'Downloading music analysis'}).getAttribute('aria-valuetext') !== '123.5 MB / 793.1 MB') throw Error('CLAP download bytes are not accessible in MB');
  await page.setViewportSize({width:390,height:850});
  await page.getByRole('progressbar',{name:'Downloading music analysis'}).scrollIntoViewIfNeeded();
  await page.screenshot({path:output+'/download-mb-narrow.png',fullPage:true});
  if(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth)) throw Error('CLAP progress overflows a narrow window');
  await page.evaluate(()=>window.__fail());
  await page.getByRole("button",{name:"Retry recommended CLAP download"}).click();
  await page.evaluate(()=>window.__validate());
  await page.getByText("Checking music analysis",{exact:true}).waitFor();
  await page.screenshot({path:output+"/validation.png",fullPage:true});
  await page.evaluate(()=>window.__finish());
  await page.getByText("CLAP compares previews with your description to help rank tracks",{exact:false}).waitFor();
  await page.screenshot({path:output+"/vocal-screening-ready.png",fullPage:true});
  if(await page.getByRole("checkbox").count()) throw Error("Uncalibrated model enabled fit decisions");
  await page.getByText("Use a custom CLAP model bundle",{exact:true}).click();
  await page.getByRole("textbox",{name:"Bundle manifest path"}).fill("/custom/bundle.json");
  await page.getByRole("button",{name:"Check bundle",exact:true}).click();
  await page.getByRole("button",{name:"Download and validate custom bundle",exact:true}).click();
  await page.getByText("123.5 MB / 793.1 MB",{exact:true}).waitFor();
  await page.getByRole("button",{name:"Stop download",exact:true}).click();
  await page.getByText("Error: Download stopped",{exact:true}).waitFor();
  if(await page.getByRole("link",{name:"Export a model to ONNX"}).getAttribute("href") !== "https://huggingface.co/docs/optimum-onnx/onnx/usage_guides/export_a_model") throw Error("Missing export documentation");
  await page.setViewportSize({width:560,height:850});
  await page.emulateMedia({reducedMotion:"reduce"});
  await page.screenshot({path:output+"/custom-narrow.png",fullPage:true});
  if(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth)) throw Error("Horizontal overflow");
  if(errors.length) throw Error(errors.join("\n"));
  console.log("PASS: one language recommendation, recommended/custom CLAP, download/retry/validation/cancel, calibration state, documentation links, themes and narrow window");
} finally { await browser.close(); }
