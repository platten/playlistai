import { describe, expect, it } from "vitest";
import { logSeverity } from "./logLevels";

describe("logSeverity", () => {
  it.each([
    ["DEBUG", -4], ["INFO", 0], ["WARN", 4], ["ERROR", 8],
    ["DEBUG+2", -2], ["INFO-2", -2], ["WARN+1", 5], ["ERROR-3", 5],
    ["", 0], ["warn", 0], ["NOTICE", 0], ["WARN+", 0], ["WARN+1extra", 0],
  ])("maps %s to its sortable severity %i", (level, severity) => {
    expect(logSeverity(level)).toBe(severity);
  });
});
