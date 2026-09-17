import { Call, type CancellablePromise } from "@wailsio/runtime";
import { API } from "./api";

export type LocalLibraryMode = "combined" | "library_only";

export interface LocalLibraryCoverage {
  tracks: number;
  metadata: number;
  mert: number;
  dsp: number;
  failed: number;
  unsupported: number;
}

export interface LocalLibraryRoot {
  alias: string;
  path?: string;
  mapped: boolean;
  available: boolean;
  detail?: string;
}

export interface LocalLibraryStatus {
  installed: boolean;
  mode: LocalLibraryMode;
  format?: string;
  version?: number;
  packId?: string;
  packSha256?: string;
  createdAt?: string;
  corpusGeneration?: string;
  metadataGeneration?: string;
  mertGeneration?: string;
  clusterGeneration?: string;
  statisticsGeneration?: string;
  coverage: LocalLibraryCoverage;
  mert?: { name?: string; dimension?: number; model?: string; sampling?: string; scope?: string };
  roots: LocalLibraryRoot[];
}

export interface LocalLibraryImportResult {
  canceled: boolean;
  status: LocalLibraryStatus;
}

type GeneratedLocalLibraryAPI = {
  GetLocalLibraryStatus?: () => CancellablePromise<LocalLibraryStatus>;
  ChooseLocalLibraryPack?: () => CancellablePromise<LocalLibraryImportResult>;
  CancelLocalLibraryImport?: () => CancellablePromise<void>;
  SetLocalLibraryMode?: (mode: string) => CancellablePromise<LocalLibraryStatus>;
  SetLocalLibraryRoot?: (alias: string, path: string) => CancellablePromise<LocalLibraryStatus>;
  RemoveLocalLibrary?: () => CancellablePromise<LocalLibraryStatus>;
};

const generated = API as unknown as GeneratedLocalLibraryAPI;
const qualified = (method: string) => `github.com/platten/playlistai/internal/bridge.API.${method}`;

function call<T>(method: keyof GeneratedLocalLibraryAPI, ...args: unknown[]): CancellablePromise<T> {
  const binding = generated[method] as ((...values: unknown[]) => CancellablePromise<T>) | undefined;
  return binding ? binding(...args) : Call.ByName(qualified(method), ...args) as CancellablePromise<T>;
}

// The name-based fallback keeps source builds usable before generated Wails
// bindings are refreshed; release builds normally take the generated-ID path.
export const LocalLibraryAPI = {
  getStatus: () => call<LocalLibraryStatus>("GetLocalLibraryStatus"),
  choosePack: () => call<LocalLibraryImportResult>("ChooseLocalLibraryPack"),
  cancelImport: () => call<void>("CancelLocalLibraryImport"),
  setMode: (mode: LocalLibraryMode) => call<LocalLibraryStatus>("SetLocalLibraryMode", mode),
  setRoot: (alias: string, path: string) => call<LocalLibraryStatus>("SetLocalLibraryRoot", alias, path),
  remove: () => call<LocalLibraryStatus>("RemoveLocalLibrary"),
};
