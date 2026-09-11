// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
const host = vi.hoisted(() => ({ render: vi.fn(), init: vi.fn(), createRoot: vi.fn() }));
vi.mock("react-dom/client", () => ({ default: { createRoot: host.createRoot } }));
vi.mock("./design/theme", () => ({ initTheme: host.init }));
vi.mock("./App", () => ({ default: "app-screen" }));
vi.mock("./screens/LogWindow", () => ({ default: "logs-screen" }));
beforeEach(() => {
  vi.resetModules();
  host.render.mockClear(); host.init.mockClear();
  host.createRoot.mockReset().mockReturnValue({ render: host.render });
  document.body.innerHTML = '<div id="root"></div>';
});
afterEach(() => { document.body.innerHTML = ""; history.replaceState(null, "", "/"); });
it.each([["/", "app-screen"], ["/?window=logs", "logs-screen"]])("routes %s to the correct window and initializes theme first", async (url, component) => {
  history.replaceState(null, "", url);
  await import("./main");
  expect(host.init).toHaveBeenCalledOnce();
  expect(host.init.mock.invocationCallOrder[0]).toBeLessThan(host.createRoot.mock.invocationCallOrder[0]);
  expect(host.render.mock.calls[0][0].props.children.type).toBe(component);
});
it("fails clearly if the desktop document has no mount element", async () => {
  document.body.innerHTML = "";
  await expect(import("./main")).rejects.toThrow("root element missing");
  expect(host.createRoot).not.toHaveBeenCalled();
});
