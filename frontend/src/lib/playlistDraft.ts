import type { PlaylistResult } from "./api";

export interface PlaylistControls {
  audioWeight: number;
  cooccurrenceWeight: number;
  discovery: number;
  artistDiversity: number;
  transitionSmoothness: number;
  count: number;
  countExplicit?: boolean;
  excludeSeedArtists: boolean | undefined;
  runSeed: string;
}

export interface PlaylistDraft {
  controls: PlaylistControls;
  accepted?: { controls: PlaylistControls; result: PlaylistResult };
}

export function sameControls(a: PlaylistControls, b: PlaylistControls): boolean {
  return (Object.keys(a) as (keyof PlaylistControls)[]).every((key) => a[key] === b[key]);
}
