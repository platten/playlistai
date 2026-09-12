# Runner preference

Windows CI and release builds prefer the registered x86-64 runner. All Linux
jobs, including Pages and winget automation, use GitHub-hosted runners:

| Work | Required labels | Current runner |
| --- | --- | --- |
| Linux amd64 test/build/package and Linux automation | `ubuntu-latest` | GitHub-hosted |
| Windows amd64 build and Windows arm64 cross-compilation | `self-hosted`, `Windows`, `X64` | `SUPERRIG` |
| Native Linux arm64 | `ubuntu-24.04-arm` | GitHub-hosted |
| Native macOS | `macos-latest` | GitHub-hosted |

Labels, rather than machine names, select jobs. Fork pull-request CI uses hosted
runners by default. The existing target matrix, artifact names and release
approval/publication flow are preserved. Windows arm64 compilation is not native
execution on arm64 hardware.

## When the Windows runner is unavailable

GitHub queues a job until a matching runner accepts it; a label list is not a
fallback preference list. There is no automatic offline-to-hosted switch here.
To temporarily use hosted Windows runners, set the repository Actions variable
`USE_HOSTED_RUNNERS` to the string `true`, then start a new workflow run. CI now
also supports **Actions → CI → Run workflow**, selecting the desired branch.
Already queued jobs retain their selected labels; cancel those runs if replacing
them. Clear the variable or set it to `false` to restore the self-hosted default.

```sh
gh variable set USE_HOSTED_RUNNERS --body true
gh workflow run ci.yml --ref <branch>
# Restore the preferred Windows runner when it is available:
gh variable set USE_HOSTED_RUNNERS --body false
```

The manual CI trigger becomes available from the Actions UI after its workflow
definition reaches the default branch. Release jobs keep their existing tag
trigger; changing runner preference does not publish a release.

## Host prerequisites

This is a persistent Windows build machine. Provision the repository's documented
development prerequisites using `scripts/setup.ps1`. Existing workflow setup steps still install the
pinned Go/Node/pnpm/Wails versions and required dependencies.

The Windows runner needs PowerShell 7, Git Bash, Chocolatey installation privileges, NSIS, and the
verified LLVM-MinGW toolchain. Release automation additionally uses its existing
`gh`, archive/signing and packaging commands. Keep runner services updated to
support the JavaScript runtime required by the pinned actions.

Self-hosting changes where build-time tools run. It does not add Python or any
other maintainer tool as an end-user application prerequisite.
