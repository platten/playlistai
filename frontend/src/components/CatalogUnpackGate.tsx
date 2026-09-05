import { useEffect, useState, type ReactNode } from "react";
import { API } from "../lib/api";
import { Download } from "./icons";
import { ProgressBar } from "./ProgressBar";
import { useProgress } from "./useProgress";

type Phase = "checking" | "unpacking" | "done";

/**
 * Wraps the whole app. When a pre-packaged, compressed catalog is staged
 * next to the binary (see cmd/catalogpack, docs/CATALOG.md) and hasn't been
 * decompressed into the data dir yet, this runs that decompression
 * automatically — no button, no network — behind a blocking popup, then
 * renders the app underneath. Every other case (already loaded, no bundled
 * archive, decompression failed) falls through to `children` immediately;
 * a failed unpack just leaves the catalog unloaded, which the rest of the
 * app already renders as an empty state.
 */
export function CatalogUnpackGate({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<Phase>("checking");
  const progress = useProgress("catalog");

  useEffect(() => {
    let cancelled = false;

    API.GetCatalogInfo()
      .then((info) => {
        if (cancelled) return;
        if (!info || info.loaded || !info.bundled) {
          setPhase("done");
          return;
        }
        setPhase("unpacking");
        return API.DownloadCatalog()
          .catch(() => undefined) // best-effort — see doc comment above
          .then(() => {
            if (!cancelled) setPhase("done");
          });
      })
      .catch(() => {
        if (!cancelled) setPhase("done");
      });

    return () => {
      cancelled = true;
    };
  }, []);

  if (phase === "unpacking") {
    return (
      <div className="grid h-full place-items-center bg-bg px-6">
        <div className="flex w-full max-w-[380px] flex-col items-center gap-5 rounded-card border border-line bg-surface px-8 py-9 text-center">
          <div className="grid size-14 place-items-center rounded-card border border-line bg-bg text-accent">
            <Download size={22} />
          </div>
          <div className="flex flex-col gap-1.5">
            <h1 className="text-[16px] font-semibold">Decompressing dataset</h1>
            <p className="text-[13px] text-muted">
              One-time setup — unpacking the bundled track catalog. This stays on your machine.
            </p>
          </div>
          <ProgressBar
            className="w-full"
            done={progress?.done ?? 0}
            total={progress?.total ?? 0}
            note={progress?.note}
          />
        </div>
      </div>
    );
  }

  if (phase === "checking") {
    // Avoid a flash of the popup (or the app) while the one check resolves.
    return <div className="h-full bg-bg" />;
  }

  return <>{children}</>;
}
