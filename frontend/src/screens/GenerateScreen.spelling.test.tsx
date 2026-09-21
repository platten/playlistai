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
function confirmSpelling(dialog: HTMLElement, choice: "suggested" | "original") {
  fireEvent.change(within(dialog).getByRole("combobox"), { target: { value: choice } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Continue" }));
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
  expect((within(dialog).getByRole("button", { name: "Continue" }) as HTMLButtonElement).disabled).toBe(true);
  confirmSpelling(dialog, "suggested");
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.ParseIntentWithContext).toHaveBeenCalledOnce();
  expect(bridge.GenerateFromPromptResolvedWithContext).toHaveBeenCalledWith(prompt,
    [{ kind: "artist", query: "christrian loeffler", trackId: "loffler-track" }], bridge.ParseIntentWithContext.mock.calls[0][1]);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value)
    .toBe("Relaxing electronic like Christian Löffler, 20 tracks");
});

it.each(["accept", "keep"])("remembers the %s decision on retry and clears it when the description changes", async (action) => {
  if (action === "accept") {
    bridge.ParseIntentWithContext.mockImplementation((text: string) => completed(
      text.includes("Christian Löffler") ? preview([]) : preview(),
    ));
  }
  renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog");
  confirmSpelling(dialog, action === "accept" ? "suggested" : "original");
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  const expected = action === "accept"
    ? { kind: "artist", query: "christrian loeffler", trackId: "loffler-track" }
    : { kind: "artist", query: "christrian loeffler", trackId: "", rejectSpelling: true };
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([expected]);
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledTimes(2));
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[1][0]).toBe(prompt);
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
  const accept = within(dialog).getByRole("button", { name: "Continue" });
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
  confirmSpelling(await screen.findByRole("dialog"), "suggested");
  const next = await screen.findByRole("dialog", { name: "Did you mean Aerosmith?" });
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  confirmSpelling(next, "original");
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
  confirmSpelling(await screen.findByRole("dialog"), "suggested");
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([
    { kind: "artist", query: "christrian loeffler", trackId: "loffler-track" },
  ]);
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value)
    .toBe("Relaxing electronic like Christian Löffler, 20 tracks, then Christian Löffler");
});

it.each(["accept", "keep"])("retains the %s spelling decision while resolving a separate ambiguous artist", async (action) => {
  const otherIssue = {
    kind: "artist", query: "Shared name", status: "ambiguous",
    alternatives: [{ entityId: "other-artist", artist: "Other artist", representatives: [{ trackId: "other-track" }] }],
  };
  bridge.ParseIntentWithContext.mockImplementation((text: string) => completed(preview(
    action === "accept" && text.includes("Christian Löffler") ? [otherIssue] : [issue, otherIssue],
  )));
  renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog");
  confirmSpelling(dialog, action === "accept" ? "suggested" : "original");
  const chooser = await screen.findByRole("combobox", { name: /Choose the intended artist/ });
  if (action === "keep") {
    expect(screen.queryByText(/Generate playlist will search MusicBrainz/)).toBeNull();
  }
  fireEvent.change(chooser, { target: { value: "other-track" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][0]).toBe(prompt);
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual(action === "accept"
    ? [
      { kind: "artist", query: "christrian loeffler", trackId: "loffler-track" },
      { kind: "artist", query: "Shared name", trackId: "other-track" },
    ] : [
      { kind: "artist", query: "christrian loeffler", trackId: "", rejectSpelling: true },
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

it("updates an uncertain artist in the description while generating with its catalog identity", async () => {
  const uncertain = "Ambient music by Shared name, 10 tracks";
  const alternatives = [
    { entityId: "other-artist", artist: "Other artist", representatives: [{ trackId: "other-track" }] },
    { entityId: "right-artist", artist: "Right artist", representatives: [{ trackId: "right-track" }] },
  ];
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([{
    kind: "artist", query: "Shared name", status: "ambiguous", inferred: false, alternatives,
  }])));
  renderScreen();
  await submit(uncertain);
  const chooser = await screen.findByRole("combobox", { name: "Choose the intended artist for “Shared name”" });
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  fireEvent.change(chooser, { target: { value: "other-track" } });
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value)
    .toBe("Ambient music by Other artist, 10 tracks");
  fireEvent.change(chooser, { target: { value: "right-track" } });
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value)
    .toBe("Ambient music by Right artist, 10 tracks");
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][0]).toBe(uncertain);
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([
    { kind: "artist", query: "Shared name", trackId: "right-track" },
  ]);
  expect(bridge.GenerateFromPromptWithContext).not.toHaveBeenCalled();
});

it("drops an uncertain artist choice when the corrected description is edited", async () => {
  bridge.ParseIntentWithContext.mockImplementation((text: string) => completed(preview(text.includes("Shared name") ? [{
    kind: "artist", query: "Shared name", status: "ambiguous", inferred: false,
    alternatives: [{ entityId: "right-artist", artist: "Right artist", representatives: [{ trackId: "right-track" }] }],
  }] : [])));
  renderScreen();
  await submit("Music by Shared name");
  fireEvent.change(await screen.findByRole("combobox", { name: /Choose the intended artist/ }), { target: { value: "right-track" } });
  fireEvent.change(screen.getByLabelText("Your description"), { target: { value: "Music by Justice" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptWithContext.mock.calls[0][0]).toBe("Music by Justice");
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
});

it("confirms a provider identity and starts generation for a homonymous artist", async () => {
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([{
    kind: "artist", query: "Shared Name", status: "ambiguous", inferred: false,
    groundingCandidates: [
      { kind: "artist", id: "artist-a", name: "Shared Name", disambiguation: "US group" },
      { kind: "artist", id: "artist-b", name: "Shared Name", disambiguation: "UK group" },
    ],
    alternatives: [],
  }])));
  renderScreen();
  await submit("Music by Shared Name");

  expect(await screen.findByText(/MusicBrainz has more than one identity/)).toBeTruthy();
  const identities = screen.getByRole("combobox", { name: /Choose the intended artist/ });
  expect(within(identities).getByText("Shared Name (US group)")).toBeTruthy();
  expect(within(identities).getByText("Shared Name (UK group)")).toBeTruthy();
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();

  const confirm = screen.getByRole("button", { name: "Confirm and generate" });
  expect((confirm as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(identities, { target: { value: "artist-b" } });
  expect(onGenerated).not.toHaveBeenCalled();
  expect(bridge.GenerateFromPromptResolvedWithContext).not.toHaveBeenCalled();
  fireEvent.click(confirm);
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([{ kind: "artist", query: "Shared Name", trackId: "", identityId: "artist-b" }]);
});

it.each(["catalog", "provider"])("preserves the offered %s identity when retrying a failed generation", async (provider) => {
  const choiceIssue = {
    kind: "artist", query: "Shared Name", status: "ambiguous", inferred: false,
    ...(provider === "provider"
      ? { groundingCandidates: [{ kind: "artist", id: "a", name: "Shared Name" }, { kind: "artist", id: "b", name: "Shared Name" }] }
      : { alternatives: [{ entityId: "a", artist: "First Artist", representatives: [{ trackId: "a" }] }, { entityId: "b", artist: "Second Artist", representatives: [{ trackId: "b" }] }] }),
  };
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([choiceIssue])));
  bridge.GenerateFromPromptResolvedWithContext.mockImplementationOnce(() =>
    Object.assign(Promise.reject(new Error("temporary lookup failure")), { cancel: vi.fn() }));
  renderScreen();
  await submit("Music by Shared Name");
  fireEvent.change(await screen.findByRole("combobox", { name: /Choose the intended artist/ }), { target: { value: "b" } });
  fireEvent.click(screen.getByRole("button", { name: "Confirm and generate" }));
  await screen.findByText(/temporary lookup failure/);
  await waitFor(() => expect((screen.getByRole("button", { name: "Confirm and generate" }) as HTMLButtonElement).disabled).toBe(false));
  expect((screen.getByRole("combobox", { name: /Choose the intended artist/ }) as HTMLSelectElement).value).toBe("b");
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptResolvedWithContext).toHaveBeenCalledTimes(2);
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[1][1]).toEqual(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]);
});

it.each(["catalog", "provider"])("clears a %s identity no longer offered by a fresh preview", async (provider) => {
  let calls = 0;
  bridge.ParseIntentWithContext.mockImplementation(() => {
    const ids = ++calls === 1 ? ["a", "b"] : ["a", "c"];
    return completed(preview([{
      kind: "artist", query: "Shared Name", status: "ambiguous", inferred: false,
      ...(provider === "provider"
        ? { groundingCandidates: ids.map((id) => ({ kind: "artist", id, name: "Shared Name" })) }
        : { alternatives: ids.map((id) => ({ entityId: id, artist: "Shared Name", representatives: [{ trackId: id }] })) }),
    }]));
  });
  bridge.GenerateFromPromptResolvedWithContext.mockImplementationOnce(() =>
    Object.assign(Promise.reject(new Error("reference choice is no longer offered")), { cancel: vi.fn() }));
  renderScreen();
  await submit("Music by Shared Name");
  fireEvent.change(await screen.findByRole("combobox", { name: /Choose the intended artist/ }), { target: { value: "b" } });
  fireEvent.click(screen.getByRole("button", { name: "Confirm and generate" }));
  await screen.findByText(/reference choice is no longer offered/);
  expect((screen.getByRole("combobox", { name: /Choose the intended artist/ }) as HTMLSelectElement).value).toBe("");
  expect((screen.getByRole("button", { name: "Confirm and generate" }) as HTMLButtonElement).disabled).toBe(true);
});

it("shows an incomplete offline recognition notice without blocking generation", async () => {
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([])));
  bridge.GenerateFromPromptWithContext.mockImplementation((_text, context) => completed({
    request: {}, name: "Playlist",
    playlist: { generationId: context.generationId, tracks: [], notices: [], outcome: { state: "needs_clarification", reasons: [] }, status: {
      state: "needs_clarification", reasons: [], partialReasons: [],
      parser: { recognitionNotices: ["Reference lookup reached its request limit; text-only parsing was used."] },
    } },
    status: {
      state: "needs_clarification", reasons: [], partialReasons: [],
      parser: { recognitionNotices: ["Reference lookup reached its request limit; text-only parsing was used."] },
    },
  }));
  renderScreen();
  await submit("Music by an unusually long artist name");
  expect(await screen.findByText("Reference lookup reached its request limit; text-only parsing was used.")).toBeTruthy();
  expect(bridge.GenerateFromPromptWithContext).toHaveBeenCalledOnce();
});

it.each(["catalog", "provider"])("keeps a %s artist match as a description, including on retry", async (provider) => {
  const descriptionIssue = {
    kind: "artist", query: "Dreamy", status: "ambiguous", inferred: false,
    ...(provider === "catalog"
      ? { alternatives: [{ entityId: "dreamy-artist", artist: "Dreamy", representatives: [{ trackId: "dreamy-track" }] }] }
      : { groundingCandidates: [{ kind: "artist", id: "a", name: "Dreamy" }, { kind: "artist", id: "b", name: "Dreamy" }] }),
  };
  bridge.ParseIntentWithContext.mockImplementation(() => completed(preview([descriptionIssue])));
  renderScreen();
  const description = "Dreamy electronic music, 10 tracks";
  await submit(description);
  const chooser = await screen.findByRole("combobox", { name: /Choose the intended artist/ });
  const option = within(chooser).getByRole("option", { name: "Keep “Dreamy” as an adjective / description" }) as HTMLOptionElement;
  fireEvent.change(chooser, { target: { value: option.value } });
  expect(onGenerated).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm and generate" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  const selection = [{ kind: "artist", query: "Dreamy", trackId: "", keepAsDescription: true }];
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0].slice(0, 2)).toEqual([description, selection]);
  expect((screen.getByLabelText("Your description") as HTMLTextAreaElement).value).toBe(description);
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledTimes(2));
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[1][1]).toEqual(selection);
  await submit(`${description}, no vocals`);
  await screen.findByRole("combobox", { name: /Choose the intended artist/ });
  expect(onGenerated).toHaveBeenCalledTimes(2);
});

it("can keep a spelling suggestion as a musical description", async () => {
  renderScreen();
  await submit();
  const dialog = await screen.findByRole("dialog");
  fireEvent.change(within(dialog).getByRole("combobox", { name: /Choose the intended artist/ }), { target: { value: "description" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Continue" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledOnce());
  expect(bridge.GenerateFromPromptResolvedWithContext.mock.calls[0][1]).toEqual([
    { kind: "artist", query: "christrian loeffler", trackId: "", keepAsDescription: true },
  ]);
  fireEvent.click(screen.getByRole("button", { name: "Generate playlist" }));
  await waitFor(() => expect(onGenerated).toHaveBeenCalledTimes(2));
  expect(screen.queryByRole("dialog")).toBeNull();
});
