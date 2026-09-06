/**
 * Re-export of the generated Wails bindings so screens don't carry the long
 * `../bindings/github.com/...` path. Regenerate with `wails3 generate bindings`.
 */
export { API } from "../../bindings/github.com/platten/playlistai/internal/bridge";
export {
  FeedbackScope,
  FeedbackType,
} from "../../bindings/github.com/platten/playlistai/internal/core";
export type {
  BuildPlaylistRequest,
  ControlOverrides,
  CatalogInfo,
  EnrichedTrackDTO,
  ExportSaveResult,
  FeedbackReceipt,
  GenerateResult,
  InstalledModel,
  IntentPreview,
  IntentSessionContext,
  LlamaRuntimeInfo,
  ModelHardwareInfo,
  ModelInfo,
  ModelRecommendations,
  ModelStatus,
  PlaylistResult,
  PlaylistTrack,
  PreviewResult,
  RecordAcceptanceRequest,
  RecordFeedbackRequest,
  ResolutionSelection,
  SavedPlaylistSummary,
  SavedPlaylist,
  SimilarResult,
  Status,
  TasteProfileSummary,
  TrackHit,
} from "../../bindings/github.com/platten/playlistai/internal/bridge";
