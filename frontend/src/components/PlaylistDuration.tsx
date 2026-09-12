import type { PlaylistResult } from "../lib/api";

function durationLabel(seconds: number) {
  const rounded = Math.round(seconds);
  return `${Math.floor(rounded / 60)}:${(rounded % 60).toString().padStart(2, "0")}`;
}

export function PlaylistDuration({ duration }: { duration: PlaylistResult["duration"] }) {
  if (!duration) return null;
  const unknown = duration.unknownTrackIds?.length ?? 0;
  const target = durationLabel(duration.targetSeconds);
  const actual = durationLabel(duration.knownMilliseconds / 1000);
  return <p className="text-[12px] text-muted" role="status">
    Target {target} ±{duration.toleranceSeconds} seconds. {unknown
      ? `${actual} known; duration unavailable for ${unknown} ${unknown === 1 ? "track" : "tracks"}. Total duration is unverified.`
      : `${actual} from recording metadata. ${duration.state === "match" ? "Within the requested range." : duration.state === "mismatch" ? "Outside the requested range." : "Total duration is unverified."}`}
  </p>;
}
