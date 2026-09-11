// Node 26 also exposes localStorage. Browser tests must use the isolated DOM
// instance instead of Node's optional, process-level experimental storage.
const dom = (globalThis as unknown as { jsdom?: { window: Window } }).jsdom;
if (dom) {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: dom.window.localStorage,
  });
}
