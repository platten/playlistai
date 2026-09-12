// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { GenerateScreen } from "./GenerateScreen";
import { PreviewPlayerProvider } from "../components/PreviewPlayer";

const bridge = vi.hoisted(() => Object.fromEntries([
  "GetCatalogInfo", "ListSavedPlaylists", "GetRecommendationMode", "ParseIntentWithContext",
  "GenerateFromPromptWithContext", "GenerateFromPromptResolvedWithContext",
].map((name) => [name, vi.fn()])));
vi.mock("../lib/api", () => ({ API: bridge }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: () => () => {} } }));

function completed(value: unknown) { return Object.assign(Promise.resolve(value), { cancel: vi.fn() }); }
function deferred() {
  let resolve!: (value: unknown) => void;
  const promise = Object.assign(new Promise((yes) => { resolve = yes; }), { cancel: vi.fn() });
  return { promise, resolve };
}
const prompt = "Relaxing electronic like christrian loeffler, 20 tracks";
const candidate = { entityId: "christian-loffler", artist: "Christian Löffler", representatives: [{ trackId: "loffler-track" }] };
const issue = {
  kind: "artist", query: "christrian loeffler", status: "ambiguous", inferred: false,
  spellingSuggestion: candidate, alternatives: [candidate],
};
function preview(issues: unknown[] = [issue]) {
  return { count: 20, creativity: 0.5, noise: 0.1, lookback: 3, intent: { preferences: {} }, resolutionIssues: issues };
}
const onGenerated = vi.fn();
function renderScreen() {
  return render(<PreviewPlayerProvider><GenerateScreen sessionId="session" parserBackend="llama" onGenerated={onGenerated} onNeedSetup={vi.fn()} /></PreviewPlayerProvider>);
}
async function submit(text = prompt) {
  fireEvent.change(await screen.findByLabelText("Your description"), { target: { value: text } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
}
beforeEach(() => {
  onGenerated.mockReset();
  for (const mock of Object.values(bridge)) mock.mockReset();
  bridge.GetCatalogInfo.mockImplementation(() => completed({ loaded: true }));
  bridge.ListSavedPlaylists.mockImplementation(() => completed([]));
  bridge.GetRecommendationMode.mockImplementation(() => completed("enhanced_hybrid"));
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview()));
  const result = (context: { generationId: string }) => completed({ request: {}, name: "Playlist", playlist: { generationId: context.generationId, tracks: [{ id: "result" }] } });
  bridge.GenerateFromPromptWithContext.mockImplementation((_text, context) => result(context));
  bridge.GenerateFromPromptResolvedWithContext.mockImplementation((_text, _selections, context) => result(context));
  vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
  HTMLDialogElement.prototype.showModal = function () { this.open = true; };
  HTMLDialogElement.prototype.close = function () { this.open = false; };
  HTMLElement.prototype.scrollIntoView = vi.fn();
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("pauses for spelling confirmation and accepts the catalog artist in the original generation context", async () => {
  renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog", { name: "Did you mean Christian Löffler?" });
  expect(bridge.GenerateFromPromptWithContext).not.toHaveBeenCalled();
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  fireEvent.click(within(dialog).getByRole("button", { name: "Use Christian Löffler" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.ParseIntentWithContext).toHaveBeenCalledOnce();
  expect(bridge.GenerateFromPromptResolvedWithContext).toHaveBeenCalledWith(prompt,
    [{ kind: "artist", query: "christrian loeffler", trackId: "loffler-track" }], bridge.ParseIntentWithContext.mock.calls[0][1]);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value).toBe(prompt);
});

it.each(["accept", "keep"])("remembers the %s decision on retry and clears it when the description changes", async (action) => {
  renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: action === "accept" ? "Use Christian Löffler" : "Keep “christrian loeffler”" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  const expected = action === "accept"
    ? { kind: "artist", query: "christrian loeffler", trackId: "loffler-track" }
    : { kind: "artist", query: "christrian loeffler", trackId: "", rejectSpelling: true };
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([expected]);
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledTimes(2));
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[1][1]).toEqual([expected]);
  expect(screen.queryByRole("dialog")).toBeNull();
  await submit(`${prompt}, no vocals`);
  await screen.findByRole("dialog");
  expect(onGenerated).toHaveBeenCalledTimes(2);
});

it.each(["button", "escape"])("cancels through %s without generating or remembering a decision", async (action) => {
  renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog");
  if (action === "button") fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  else fireEvent(dialog, new Event("cancel", { bubbles: false, cancelable: true }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  expect(bridge.GenerateFromPromptWithContext).not.toHaveBeenCalled();
  expect((screen.getByRole("button", { name: "Generate playlist" }) as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await screen.findByRole("dialog");
});

it("dismisses a pending confirmation on an edited description and ignores cancelled parse responses", async () => {
  renderScreen();
  await submit();
  await screen.findByRole("dialog");
  fireEvent.change(screen.getByLabelText("Your description"), { target: { value: "Aerosmith" } });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  const pending = deferred();
  bridge.ParseIntentWithContext.mockReturnValueOnce(pending.promise);
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await act(async () => { pending.resolve(preview()); });
  expect(pending.promise.cancel).toHaveBeenCalled();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(onGenerated).not.toHaveBeenCalled();
});

it("does not resume a pending confirmation after unmount", async () => {
  const view = renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog");
  const accept = within(dialog).getByRole("button", { name: "Use Christian Löffler" });
  view.unmount();
  await act(async () => { fireEvent.click(accept); });
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  expect(onGenerated).not.toHaveBeenCalled();
});

it("collects multiple spelling decisions before generation", async () => {
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([issue, {
    ...issue, query: "aerosmit", spellingSuggestion: { entityId: "aerosmith", artist: "Aerosmith", representatives: [{ trackId: "aerosmith-track" }] },
  }])));
  renderScreen();
  await submit();
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Use Christian Löffler" }));
  const next = await screen.findByRole("dialog", { name: "Did you mean Aerosmith?" });
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  fireEvent.click(within(next).getByRole("button", { name: "Keep “aerosmit”" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([
    { kind: "artist", query: "christrian loeffler", trackId: "loffler-track" },
    { kind: "artist", query: "aerosmit", trackId: "", rejectSpelling: true },
  ]);
});

it("confirms a repeated artist with different capitalization only once", async () => {
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([issue, { ...issue, query: "Christrian Loeffler" }])));
  renderScreen();
  await submit(`${prompt}, then Christrian Loeffler`);
  fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Use Christian Löffler" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([
    { kind: "artist", query: "christrian loeffler", trackId: "loffler-track" },
  ]);
});

it.each(["accept", "keep"])("retains the %s spelling decision while resolving a separate ambiguous artist", async (action) => {
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([issue, {
    kind: "artist", query: "Shared name", status: "ambiguous", alternatives: [{ entityId: "other-artist", artist: "Other artist", representatives: [{ trackId: "other-track" }] }],
  }])));
  renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: action === "accept" ? "Use Christian Löffler" : "Keep “christrian loeffler”" }));
  const chooser = await screen.findByRole("combobox");
  if (action === "keep") {
    expect(screen.queryByText(/Generate playlist will search MusicBrainz/)).toBeNull();
  }
  fireEvent.change(chooser, { target: { value: "other-track" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([
    action === "accept" ? { kind: "artist", query: "christrian loeffler", trackId: "loffler-track" }
      : { kind: "artist", query: "christrian loeffler", trackId: "", rejectSpelling: true },
    { kind: "artist", query: "Shared name", trackId: "other-track" },
  ]);
});

it("generates exact artist references without opening the dialog", async () => {
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([])));
  renderScreen();
  await submit("Relaxing electronic like Christian Löffler, 20 tracks");
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledOnce();
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
});
