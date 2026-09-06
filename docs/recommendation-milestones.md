# Recommendation Milestones

## Milestone 1 — Correctness

Implemented against baseline `4c100e7`. Recommendation walks now keep reference
tracks separate from required output tracks, interpret `count` as the total
playlist length, distribute journey intermediates across segments, and use the
same normalized artist/title recording identity in similar and journey modes.
Hard artist and seed-artist exclusions are never bypassed. Exhausted candidate
sets return a partial playlist with an `eligible_tracks_exhausted` notice;
required-track exclusion conflicts and counts below the required waypoint count
return explicit errors.

Intent and bridge request version 2 add explicit required/reference fields.
Version 1 `seeds` and `seedIds` migrate to both roles, preserving the historical
behavior of saved requests. Upstream journey golden files remain checked in as
a documented parity baseline but are skipped because their count means
intermediates per segment.

Next dependencies: canonical recording IDs from richer catalog metadata,
clearer required-track authoring controls, and product decisions about placing
required tracks that are not journey waypoints. Semantic models and
personalization remain out of scope.

## Milestone 2 — Intent Preservation

The version 3 intent contract carries typed positive/negative artist and track
references, distinct required tracks, semantic preferences, explicit hard
constraints, journey and energy intent, source evidence, unsupported strict
requirements, and independent generation controls. The bridge now rebuilds
from the complete resolved intent plus explicit control overrides, so slider
changes cannot discard parser meaning. History loading migrates v1/v2 request
and intent JSON into v3; saved v3 records replay without re-parsing prompts.

Current execution support is declared on every normalized intent. Positive
catalog references, required tracks, total count, exact artist exclusions, discovery
variation, and audio/co-occurrence weighting are supported. Transition
smoothness is limited to walk memory/interpolation; artist diversity is limited
to the separate no-back-to-back constraint. Style, mood, instrumentation,
vocals, free-text texture, and energy trajectories are preserved with evidence
but are not scored or advertised as enforced.

Next dependencies: catalog features or a local semantic scorer for preferences
and energy, canonical recording/artist identities, and a diversity-aware
selection objective, including general negative-reference scoring. These capabilities must update their declared status when
implemented.
