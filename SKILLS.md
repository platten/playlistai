# Repository Skills and Delivery Workflow

This guide describes how contributors and coding agents deliver changes to
PlaylistAI. Read [AGENTS.md](AGENTS.md) and any applicable directory instructions
first. Use this document alongside those instructions and the task requirements.

## Inspect and Scope the Work

- Check the current branch, `git status --short`, relevant code, and recent
  commits before editing. Preserve unrelated work, including uncommitted files.
- Define the intended user-visible result and how to verify it. Reproduce bugs
  against the current code before implementing a fix.
- Keep each change focused. Ask for clarification when missing information
  materially affects behavior or compatibility; resolve routine implementation
  details using the existing architecture and conventions.

## Implement and Verify

Keep domain logic in `internal/`, bridge contracts in `internal/bridge/`, and
React screens and components in `frontend/src/`. Format Go with `gofmt`; follow
the existing TypeScript style and design tokens. Regenerate Wails bindings when
contracts change; never edit generated bindings manually.

Add focused regression coverage for changed behavior. Preserve intentional
baseline fixtures and explain any updates. Run relevant package checks while
working, then the repository gate before submission:

```sh
# Linux/macOS, from the repository root
./scripts/test.sh
git diff --check
```

On Windows use `.\scripts\test.ps1`. Report skipped checks and missing tools even
if the script exits successfully. For desktop or packaging changes, identify the
platforms actually tested. For UI changes, inspect the rendered screen and
capture relevant screenshots when the environment supports it.

## Write Conventional Commits

Use [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/)
for new commits and PR titles:

```text
type(scope): imperative description

Explain why the change is needed and any meaningful tradeoffs.

Refs: #issue-number
```

Use `feat` for features, `fix` for bugs, `perf` for performance improvements,
`refactor` for restructuring, `docs` for documentation, `test` for tests, and
`build`, `ci`, or `chore` for supporting work. Scope is optional; use a meaningful
area such as `reco`, `intent`, `audio`, or `ui`.

Examples:

```text
fix(reco): preserve artist exclusions during candidate refill
feat(ui): add detailed recommendation diagnostics
docs: explain CLAP preview coverage and limitations
```

Keep subjects concise and commits independently understandable. Mark incompatible
changes with `!` or a `BREAKING CHANGE:` footer; document the migration. Existing
history uses mixed styles, so apply this convention going forward without
rewriting published commits. Inspect `git diff --cached` before committing and
stage only files or hunks belonging to the change.

## Create and Maintain Pull Requests

Use a descriptive topic branch such as `fix/catalog-seed-resolution`. When
delivering through a PR, confirm the target repository and base branch, push the
topic branch, and open a focused PR. Use a draft while substantive work or
validation remains. Check for an existing PR before creating another.

Follow [GitHub's review guidance](https://docs.github.com/en/pull-requests/concepts/helping-others-review-your-changes):
explain the problem, resulting behavior, and reviewer attention points. Keep the
title and description current as the implementation changes. Address review
feedback, rerun affected checks, inspect CI results, and distinguish local passes
from hosted CI results. Treat merging, publishing releases, and deployment as
separate delivery actions with their own task scope.

## Document PRs Thoroughly

Write for a reviewer who has not seen the conversation. Use the following
structure, omitting inapplicable sections and replacing every placeholder:

```markdown
## Problem and Result

Describe the user-facing problem, reproduction input, and before/after behavior.
Link related issues; use a closing reference only when this PR resolves one.

## Implementation

Explain the design, important affected components, and non-obvious tradeoffs.
Identify intentional fixture, generated-file, or dependency changes.

## Validation

List executed commands and actual results. Include reproducible benchmark
conditions, UI screenshots, tested platforms, and any blocked or skipped checks.

## Compatibility and Risks

Describe saved-history, schema, cache, model, and packaging impacts where relevant.
Explain migrations, remaining limitations, and recovery steps for risky changes.

## Documentation

Link updated usage instructions, architecture notes, and milestone records.
```

## Keep Documentation and Evidence Trustworthy

Update `README.md` for usage changes and `docs/` for architecture, compatibility,
and milestone decisions. Update `site/` when public claims or instructions need
to change. Use a small Mermaid diagram when it clarifies a multi-stage flow.

Record benchmark commands, commit, hardware, catalog/model versions, inputs,
and measured results. Distinguish synthetic regression checks from musical
quality evidence. Describe unknown or unsupported musical attributes accurately.

Preserve the local-first design. Keep downloaded models, catalogs, credentials,
private logs, prompts, and user data out of commits and PR attachments. Redact
screenshots and diagnostic excerpts before sharing them.
