/** Saved playlists may still use historical policies; keep their labels exact. */
export function recommendationModeLabel(mode: string): string {
  switch (mode) {
    case "enhanced_hybrid": return "Enhanced hybrid";
    case "deejai_only": return "Deej-AI only";
    case "clap_first": return "CLAP first";
    case "acousticbrainz_first": return "AcousticBrainz first";
    default: return "Unknown recommendation mode";
  }
}
