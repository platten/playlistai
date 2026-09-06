/**
 * Re-export of the generated Wails bindings so screens don't carry the long
 * `../bindings/github.com/...` path. Regenerate with `wails3 generate bindings`.
 */
export { API } from "../../bindings/github.com/platten/playlistai/internal/bridge";
export type {
  BuildPlaylistRequest,
  ControlOverrides,
  CatalogInfo,
  EnrichedTrackDTO,
  ExportSaveResult,
  GenerateResult,
  InstalledModel,
  IntentPreview,
  LlamaRuntimeInfo,
  ModelInfo,
  ModelStatus,
  PlaylistResult,
  PlaylistTrack,
  PreviewResult,
  SavedPlaylistSummary,
  SavedPlaylist,
  SimilarResult,
  Status,
  TrackHit,
} from "../../bindings/github.com/platten/playlistai/internal/bridge";
