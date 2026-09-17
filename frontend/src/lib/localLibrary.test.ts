import { beforeEach, expect, it, vi } from "vitest";

const byName = vi.hoisted(() => vi.fn());
vi.mock("./api", () => ({ API: {} }));
vi.mock("@wailsio/runtime", () => ({ Call: { ByName: byName } }));

import { LocalLibraryAPI } from "./localLibrary";

beforeEach(() => {
  byName.mockReset().mockResolvedValue({ installed: false, mode: "combined", coverage: {}, roots: [] });
});

it("uses fully qualified Wails names before generated bindings are refreshed", async () => {
  await LocalLibraryAPI.getStatus();
  expect(byName).toHaveBeenLastCalledWith("github.com/platten/playlistai/internal/bridge.API.GetLocalLibraryStatus");
  await LocalLibraryAPI.setMode("library_only");
  expect(byName).toHaveBeenLastCalledWith("github.com/platten/playlistai/internal/bridge.API.SetLocalLibraryMode", "library_only");
  await LocalLibraryAPI.setRoot("music-main", "/mnt/music");
  expect(byName).toHaveBeenLastCalledWith("github.com/platten/playlistai/internal/bridge.API.SetLocalLibraryRoot", "music-main", "/mnt/music");
});
