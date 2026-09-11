import { useEffect, useState } from "react";
import { API, RecommendationMode } from "../lib/api";

const options = [
  {
    mode: RecommendationMode.AcousticBrainzFirst,
    title: "AcousticBrainz first",
    detail: "Prefer decisive archived predictions for supported characteristics. CLAP covers missing or uncertain evidence.",
  },
  {
    mode: RecommendationMode.CLAPFirst,
    title: "CLAP first",
    detail: "Prefer preview-to-description comparisons. AcousticBrainz adds evidence for characteristics the preview could not score.",
  },
  {
    mode: RecommendationMode.DeejAIOnly,
    title: "Deej-AI only",
    detail: "Use the original embedding walk from catalog artists or tracks. No metadata enrichment, CLAP, AcousticBrainz, personalization or MMR selection.",
  },
];

export function RecommendationSettings() {
  const [mode, setMode] = useState<RecommendationMode | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    const call = API.GetRecommendationMode();
    let current = true;
    call.then((value) => { if (current) setMode(value); })
      .catch((e) => { if (current) setError(String(e)); });
    return () => { current = false; void call.cancel("settings closed"); };
  }, []);

  const choose = async (value: RecommendationMode) => {
    if (saving || mode === value) return;
    setSaving(true);
    setError("");
    try {
      await API.SetRecommendationMode(value);
      setMode(value);
    } catch (e) {
      setError(String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-[12px] font-semibold tracking-[0.08em] text-muted uppercase">Recommendations</h2>
      <fieldset disabled={saving || mode === null} className="flex flex-col gap-2" aria-busy={saving}>
        <legend className="mb-3 text-[12px] text-muted">Choose which evidence leads recommendation ranking.</legend>
        {options.map((option) => (
          <label key={option.mode} className={`flex cursor-pointer items-start gap-3 rounded-card border px-4 py-3.5 transition-colors ${mode === option.mode ? "border-accent/60 bg-accent-quiet" : "border-line bg-surface hover:border-line-strong hover:bg-hover"} ${saving ? "opacity-60" : ""}`}>
            <input type="radio" name="recommendation-mode" className="mt-1 accent-[var(--pai-accent)]" value={option.mode} checked={mode === option.mode} onChange={() => void choose(option.mode)} />
            <span>
              <span className="block text-[13.5px] font-medium">{option.title}</span>
              <span className="mt-1 block text-[12px] text-muted">{option.detail}</span>
            </span>
          </label>
        ))}
      </fieldset>
      <p className="text-[12px] text-faint" role="status">{saving ? "Saving…" : "Applies to new playlists and Regenerate. Saved playlists keep their original mode; active generations are unchanged."}</p>
      <p className="text-[12px] text-muted">{mode === RecommendationMode.DeejAIOnly
        ? "Descriptions are still parsed locally, but a catalog reference is needed. Genre and mood fit are unverified. Unsupported strict requirements return an explanation, not an unchecked playlist. The artist-diversity slider does not affect the original walk."
        : "Priority changes ranking, not hard requirements. Both sources may still be checked, and disagreements remain visible. Evidence availability varies by recording."}</p>
      {error && <p role="alert" className="text-[12px] text-warn">{error}</p>}
    </section>
  );
}
