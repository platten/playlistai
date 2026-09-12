# Workflow runners

All CI, release, Pages and winget jobs use GitHub-hosted runners. No self-hosted
runner registration or local build machine is required.

| Work | Runner label |
| --- | --- |
| Linux amd64 test/build/package and Linux automation | `ubuntu-latest` |
| Windows amd64 build and Windows arm64 cross-compilation | `windows-latest` |
| Native Linux arm64 | `ubuntu-24.04-arm` |
| Native macOS | `macos-latest` |

Windows arm64 compilation is not native execution on arm64 hardware. The existing
target matrix, artifact names and release approval/publication flow are preserved.
Workflow setup steps install the pinned tools and required platform dependencies.
These build-time tools do not add Python or other maintainer prerequisites to the
end-user application.

CI supports manual dispatch through **Actions → CI → Run workflow** once the
workflow definition reaches the default branch. Release jobs retain their tag
trigger. The former `USE_HOSTED_RUNNERS` variable is no longer used.
