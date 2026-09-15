# Updates and setup readiness

The startup update prompt displays the GitHub release notes under “What’s new”.
Notes preserve line breaks and scroll independently on small windows. They are
displayed as escaped text; embedded HTML is never executed. The Release page
action opens the full GitHub release, including when notes are unavailable.

![Startup release notes](images/update-release-notes.png)

Installing an application update does not reset onboarding. At startup, a local,
read-only readiness check identifies missing assets for previously selected
capabilities. A completed installation opens normally when those assets are
available. If a repair is needed, the wizard shows only affected steps. New
optional features alone do not reopen a completed wizard.

First setup requires every supported catalog, metadata, language-model, intent,
music-analysis, MERT, and preview step. Already-ready steps are omitted and
installed assets are checked before a download is offered. MERT and DSP analysis
are enabled by default. There is no skip action. A final readiness check precedes
completion; a failed preferences save leaves the wizard open with a retry action.
Unsupported capabilities are omitted.

Previously completed installations retain deliberately selected rules-only
parsing and disabled previews when repairing missing assets. These compatibility
choices do not let a fresh installation bypass required setup.

![Setup showing only a missing model repair](images/setup-repair.png)

The readiness query uses activated state, local bundle metadata, file presence,
expected sizes and a bounded GGUF header check. The overall startup also verifies
audio pack hashes and launches native workers for health checks, asynchronously.
Window construction no longer waits for these operations. Pending validation
is shown as loading, with download and completion disabled until the result is
known. Cancellation and stale-operation protection prevent an old startup result
from replacing a newer selection. Startup does not download assets automatically.

Clearing the language model now persists `modelDisabled` in preferences. Older
preferences retain their existing configured-model behavior; selecting a model
again clears this flag. Python remains limited to offline tooling.

## Verification

Go regressions cover readiness and release-note propagation. Frontend tests
cover startup routing, repair-only setup, already-ready steps, loading validation,
completion-save failures, and update states.
`scripts/capture-update-prompt.mjs` and `scripts/capture-setup-readiness.mjs`
exercise browser fixtures at 390 and 1000 pixels in both themes. These checks do
not execute a native installer or establish native execution on other OSes.
