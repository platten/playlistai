# Application logs

Open **Settings → Application logs → Open logs** to inspect the current session
in a separate window. Choose **Minimum level** to show that severity and higher:

| Minimum | Visible records |
| --- | --- |
| DEBUG | Debug details, information, warnings, and errors |
| INFO | Information, warnings, and errors (default) |
| WARN | Warnings and errors |
| ERROR | Errors only |

Selecting DEBUG enables the existing **Show detailed recommendation diagnostics**
preference. It captures ordinary Go DEBUG messages as well as structured prompt,
parser, provider, CLAP, AcousticBrainz, and track-selection diagnostics when those
stages run. Existing logs cannot reconstruct details from before collection was
enabled. This does not enable debug output to the console or create a log file.

Selecting INFO, WARN, or ERROR disables debug collection and removes retained
DEBUG entries. Non-debug records remain retained: switching from ERROR back to
INFO reveals earlier information and warnings. The footer distinguishes visible
from retained records, and an empty filtered view explains the selected level.

The debug opt-in persists across restarts and is shared with Settings. WARN and
ERROR are viewer-only filters and reset to INFO when the window reopens. Changes
from Settings appear on the next successful log refresh; Settings refreshes its
checkbox when its window regains focus. While a preference is saving the dropdown
is disabled; failed saves display an error without claiming the change succeeded.

## Privacy and retention

DEBUG data can contain full prompts, model output, provider lookup terms,
listening context, and analysis vectors. Review excerpts before sharing them.
Collection is off by default, and the window warns while it is enabled.

The backend keeps at most 2,000 records or 16 MiB of log text in memory; individual
structured diagnostics are capped at 64 KiB. Oversized records are explicitly
marked as truncated. The viewer displays up to 2,000 retained records. Turning
off DEBUG clears debug records; closing the log window alone does not disable
collection. Closing the application discards session logs. Saved history and
derived musical analysis have separate storage and clearing controls.

**Follow new entries** scrolls to incoming records. Scrolling up pauses following
so older entries can be read. Connection failures retain the displayed records
and retry automatically. Closing the log window leaves the main app open.

## Developer checks

```sh
go test -race ./internal/logging ./internal/bridge
./scripts/test.sh

# With Vite running on port 9245 and a working local Chromium installation:
node scripts/capture-log-window.mjs /path/to/playwright/index.mjs /path/to/chromium
```

The browser check uses synthetic local records, not private prompts or real
provider calls. It covers minimum-level filtering, opt-in persistence, custom
slog levels, pending/failed writes, stale polling responses, external preference
changes, scrolling, empty/error states, and narrow layouts in both themes.
