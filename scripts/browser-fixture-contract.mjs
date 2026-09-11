import { readFileSync } from "node:fs";

// Derive public string-enum exports from the checked-in generated contract.
// Browser fixtures must not silently substitute obsolete enum member names.
const source = readFileSync(new URL("../frontend/bindings/github.com/platten/playlistai/internal/core/models.ts", import.meta.url), "utf8");
export const bridgeEnums = ["RecommendationMode", "FeedbackScope", "FeedbackType"].map((name) => {
  const body = source.match(new RegExp(`export enum ${name} \\{([\\s\\S]*?)\\};`))?.[1];
  if (!body) throw new Error(`Generated bridge enum missing: ${name}`);
  const entries = [...body.matchAll(/([\w$]+)\s*=\s*("(?:[^"\\]|\\.)*")/g)]
    .map(([, key, value]) => [key, JSON.parse(value)]);
  if (!entries.length) throw new Error(`Generated bridge enum has no string values: ${name}`);
  return `export const ${name} = ${JSON.stringify(Object.fromEntries(entries))};`;
}).join("\n");
