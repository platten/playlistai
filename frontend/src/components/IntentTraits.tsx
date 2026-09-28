type Trait = {
  kind?: string;
  value: string;
  influence?: string;
  degree?: string;
  scope?: string;
  group?: string;
  strength?: string;
};

type Preferences = {
  genres?: Trait[] | null;
  styles?: Trait[] | null;
  moods?: Trait[] | null;
  instrumentation?: Trait[] | null;
  textureDescriptions?: Trait[] | null;
  vocalPreference?: Trait | null;
  vocalPreferences?: Trait[] | null;
};

function traitText(p: Trait) {
  const degree = p.degree === "mostly" ? "mostly " : p.degree === "reduced" ? "less " : p.influence === "negative" ? "avoid " : "";
  const scope = p.scope === "journey_start" ? " at the start" : p.scope === "journey_end" ? " at the end" : p.scope === "journey_via" ? " in the middle" : "";
  return `${degree}${p.value}${scope}`;
}

// A group can span facets. Show it once, with its alternatives together, so
// the preview does not turn "piano or ambient" into two independent demands.
function groupedText(items: Trait[], format: (item: Trait) => string) {
  const units: Trait[][] = [];
  const groups = new Map<string, number>();
  for (const item of items) {
    const key = `${item.scope || "playlist"}\u0000${item.group}`;
    const existing = item.group ? groups.get(key) : undefined;
    if (existing !== undefined) units[existing].push(item);
    else {
      if (item.group) groups.set(key, units.length);
      units.push([item]);
    }
  }
  return units.map((unit) => unit.map(format).join(" or "));
}

export function IntentTraits({ preferences, criteria = [], automatic = false }: { preferences: Preferences; criteria?: Trait[]; automatic?: boolean }) {
  const vocals = preferences.vocalPreferences?.length ? preferences.vocalPreferences : preferences.vocalPreference ? [preferences.vocalPreference] : [];
  const facets: [string, Trait[]][] = [
    ["Genres", preferences.genres ?? []],
    ["Vocals", vocals],
    ["Instrumentation", preferences.instrumentation ?? []],
    ["Character", [...(preferences.styles ?? []), ...(preferences.moods ?? []), ...(preferences.textureDescriptions ?? [])]],
  ];
  const alternatives = facets.flatMap(([, items]) => items.filter((item) => item.group));
  const estimated = (item: Trait) => ["mood", "instrumentation", "texture"].includes(item.kind ?? "") && ["essential", "preferred"].includes(item.strength ?? "");
  const character = criteria.filter((item) => automatic && estimated(item) && (!item.group || !criteria.some((other) => other.group === item.group && other.scope === item.scope && !estimated(other))));
  const strict = criteria.filter((item) => !character.includes(item));
  const formatCriterion = (item: Trait) => `${item.kind === "composer" ? "composed by " : ""}${item.value}${item.scope?.startsWith("journey_") ? ` (${item.scope.replace("journey_", "")})` : ""}`;
  return <>
    {facets.map(([label, items]) => {
      const text = items.filter((item) => !item.group).map(traitText).join(" · ");
      return text ? <p key={label}>{label}: {text}</p> : null;
    })}
    {alternatives.length > 0 && <p>Alternatives: {groupedText(alternatives, traitText).join(" · ")}</p>}
    {character.length > 0 && <p>Musical character (estimated): {groupedText(character, formatCriterion).join(", ")}</p>}
    {strict.length > 0 && <p>{automatic ? "Strict requirements" : "Essential"}: {groupedText(strict, formatCriterion).join(", ")}</p>}
  </>;
}
