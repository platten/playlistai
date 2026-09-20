// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { DiscoveryDataCard } from "./DiscoveryDataCard";

const api = vi.hoisted(() => ({ GetDiscoveryAssetStatus: vi.fn(), InstallDiscoveryAsset: vi.fn(), CancelDiscoveryAssetInstall: vi.fn(), CheckDiscoveryAssetUpdate: vi.fn(), ChooseDiscoveryPack:vi.fn(),ChooseDiscoveryArchiveFolder:vi.fn(),CancelDiscoveryArchive:vi.fn() }));
vi.mock("../lib/api", () => ({ API: api }));
vi.mock("./useProgress", () => ({ useProgress: () => null }));
const empty = { configured: true, hostedConfigured:true, installed: false, version: "", tracks: 0, downloadBytes: 0, source:"",manifestDigest:"" };
function completed<T>(value: T) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = Object.assign(new Promise<T>((yes) => { resolve = yes; }), { cancel: vi.fn() });
  return { promise, resolve };
}
beforeEach(() => { vi.clearAllMocks(); api.GetDiscoveryAssetStatus.mockImplementation(() => completed(empty)); });
afterEach(cleanup);

it("keeps setup unready until installation is verified", async () => {
  const ready = vi.fn();
  const pending = deferred<typeof empty>();
  api.InstallDiscoveryAsset.mockReturnValue(pending.promise);
  render(<DiscoveryDataCard setup onReadyChange={ready} />);
  fireEvent.click(await screen.findByRole("button", { name: "Download or resume discovery data" }));
  expect(ready).toHaveBeenLastCalledWith(false);
  expect(screen.getByRole("button", { name: "Cancel download" })).toBeTruthy();
  await act(async () => { pending.resolve({ ...empty, installed: true, tracks: 42, version: "test-v1" }); });
  expect(ready).toHaveBeenLastCalledWith(true);
  expect(screen.getByText("42 recordings ready · test-v1")).toBeTruthy();
});

it("cancels on unmount and ignores the late result", async () => {
  const ready = vi.fn();
  const pending = deferred<typeof empty>();
  api.InstallDiscoveryAsset.mockReturnValue(pending.promise);
  const view = render(<DiscoveryDataCard setup onReadyChange={ready} />);
  fireEvent.click(await screen.findByRole("button", { name: "Download or resume discovery data" }));
  view.unmount();
  expect(pending.promise.cancel).toHaveBeenCalled();
  await act(async () => { pending.resolve({ ...empty, installed: true }); });
  expect(ready).toHaveBeenLastCalledWith(false);
});

it("keeps a verified release available after an update failure", async () => {
  api.GetDiscoveryAssetStatus.mockImplementation(() => completed({ ...empty, installed: true, tracks: 42, version: "old" }));
  api.InstallDiscoveryAsset.mockImplementation(() => Object.assign(Promise.reject(new Error("checksum mismatch")), { cancel: vi.fn() }));
  const ready = vi.fn();
  render(<DiscoveryDataCard onReadyChange={ready} />);
  fireEvent.click(await screen.findByRole("button", { name: "Check and install latest release" }));
  expect((await screen.findByRole("alert")).textContent).toContain("checksum mismatch");
  expect(screen.getByText("42 recordings ready · old")).toBeTruthy();
  expect(ready).toHaveBeenLastCalledWith(true);
});

it("checks the release size without starting an installation", async () => {
  api.CheckDiscoveryAssetUpdate.mockImplementation(() => completed({ version: "next", downloadBytes: 1200000000, updateAvailable: true }));
  render(<DiscoveryDataCard setup />);
  fireEvent.click(await screen.findByRole("button", { name: "Check release size and updates" }));
  expect(await screen.findByText("Release download: 1.20 GB · next.")).toBeTruthy();
  expect(api.InstallDiscoveryAsset).not.toHaveBeenCalled();
});

it("validates a local override before marking setup ready and switches back explicitly",async()=>{
  const ready=vi.fn();const pending=deferred<{canceled:boolean;status:typeof empty}>();
  api.ChooseDiscoveryPack.mockReturnValue(pending.promise);
  api.InstallDiscoveryAsset.mockImplementation(()=>completed({...empty,installed:true,source:"hosted",version:"hosted-v1",tracks:40}));
  render(<DiscoveryDataCard onReadyChange={ready}/>);
  fireEvent.click(await screen.findByRole("button",{name:"Choose local .paipack"}));
  expect(ready).toHaveBeenLastCalledWith(false);
  expect(screen.getByRole("button",{name:"Cancel import"})).toBeTruthy();
  await act(async()=>pending.resolve({canceled:false,status:{...empty,installed:true,source:"local",version:"local-v1",tracks:5}}));
  expect(ready).toHaveBeenLastCalledWith(true);
  expect(screen.getByText(/Source: Local pack override/)).toBeTruthy();
  expect(api.InstallDiscoveryAsset).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button",{name:"Use hosted discovery data"}));
  expect(await screen.findByText(/Source: Hosted music collection/)).toBeTruthy();
});

it("does not change the active source when picker is canceled or import fails",async()=>{
  api.GetDiscoveryAssetStatus.mockImplementation(()=>completed({...empty,installed:true,source:"hosted",version:"active",tracks:10}));
  api.ChooseDiscoveryPack.mockImplementationOnce(()=>completed({canceled:true})).mockImplementationOnce(()=>Object.assign(Promise.reject(new Error("invalid pack")),{cancel:vi.fn()}));
  render(<DiscoveryDataCard/>);
  fireEvent.click(await screen.findByRole("button",{name:"Choose local .paipack"}));
  await act(async()=>{});
  expect(screen.getByText("10 recordings ready · active")).toBeTruthy();
  fireEvent.click(screen.getByRole("button",{name:"Choose local .paipack"}));
  expect((await screen.findByRole("alert")).textContent).toContain("invalid pack");
  expect(screen.getByText("10 recordings ready · active")).toBeTruthy();
});

it("saves an offline manifest and parts without activating a new source",async()=>{
  api.GetDiscoveryAssetStatus.mockImplementation(()=>completed({...empty,installed:true,source:"local",version:"local",tracks:10}));
  api.ChooseDiscoveryArchiveFolder.mockImplementation(()=>completed({canceled:false,archive:{files:3,directory:"/chosen/new-archive"}}));
  render(<DiscoveryDataCard/>);
  fireEvent.click(await screen.findByRole("button",{name:"Save manifest and parts for offline use"}));
  expect(await screen.findByText("Saved 3 files (manifest and archive parts) to /chosen/new-archive")).toBeTruthy();
  expect(screen.getByText("10 recordings ready · local")).toBeTruthy();
  expect(api.InstallDiscoveryAsset).not.toHaveBeenCalled();
});

it("ignores an import completing after unmount",async()=>{
  const pending=deferred<{canceled:boolean;status:typeof empty}>();
  const ready=vi.fn();api.ChooseDiscoveryPack.mockReturnValue(pending.promise);
  const view=render(<DiscoveryDataCard onReadyChange={ready}/>);
  fireEvent.click(await screen.findByRole("button",{name:"Choose local .paipack"}));
  view.unmount();expect(pending.promise.cancel).toHaveBeenCalled();
  await act(async()=>pending.resolve({canceled:false,status:{...empty,installed:true,source:"local"}}));
  expect(ready).toHaveBeenLastCalledWith(false);
});

it("cancels an offline download without changing readiness",async()=>{
  const pending=deferred<unknown>();const ready=vi.fn();
  api.GetDiscoveryAssetStatus.mockImplementation(()=>completed({...empty,installed:true,source:"local",version:"local",tracks:10}));
  api.ChooseDiscoveryArchiveFolder.mockReturnValue(pending.promise);
  render(<DiscoveryDataCard onReadyChange={ready}/>);
  fireEvent.click(await screen.findByRole("button",{name:"Save manifest and parts for offline use"}));
  fireEvent.click(screen.getByRole("button",{name:"Cancel download"}));
  expect(pending.promise.cancel).toHaveBeenCalled();expect(api.CancelDiscoveryArchive).toHaveBeenCalled();
  expect(ready).toHaveBeenLastCalledWith(true);
});
