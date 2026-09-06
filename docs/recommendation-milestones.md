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
