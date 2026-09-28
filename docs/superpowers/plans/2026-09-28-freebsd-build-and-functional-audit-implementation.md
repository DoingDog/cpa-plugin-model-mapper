# FreeBSD Build and Functional Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the FreeBSD/amd64 CI build and prevent aggregate packaging from overwriting input plugin libraries through output aliases, then ship a verified `v0.5.7` patch release.

**Architecture:** The FreeBSD job replaces only the obsolete third-party cross-build step with a pinned, SHA-verified FreeBSD 14.4 sysroot and an explicit Go `c-shared` cross-build. The aggregate packager reuses its existing file-identity validator once for every input and possible output before changing any file. Neither task changes CPA or the plugin's model-routing behavior.

**Tech Stack:** Go 1.26, stdlib `os`/`filepath` tests, GNU Make, GitHub Actions Bash on Ubuntu 24.04, Clang/lld, FreeBSD 14.4 base distribution.

**Spec:** `docs/superpowers/specs/2026-09-28-freebsd-build-and-functional-audit-design.md`

## Global Constraints

- Modify this plugin repository only; do not change CPA source or read unrelated CPA code.
- The latest published release is `v0.5.6`; the next patch tag is `v0.5.7` only after the complete main CI succeeds.
- FreeBSD target remains only `freebsd/amd64`, with `-buildmode=c-shared` and the existing archive/package/upload/release dependencies.
- Pin the official FreeBSD 14.4 amd64 `base.txz` SHA256 to `769f60a6eea2938ad6b7943cfbcc17dfaf7d1f7ba59a32b869a00349302df853` and verify it before extraction.
- Preserve existing plugin stream, scoped-rule, and live-reconfiguration behavior; do not pretend a request-affine host token is available when CPA does not provide one.
- Tests use temporary files only; retain original checkout's untracked `.claude/` and ignored `dist/`/`.test-cpa/`.
- Do not read pre-existing, unrelated plans or specs. Preserve the user's no-question, unattended authorization while reporting failures accurately.

## Review Focus

1. An aggregate zip symlink points to its own input library: reject before truncation; Task 1 tests the symlink alias and unchanged bytes.
2. An aggregate zip hardlink points to a *different* platform's input: reject before the first platform writes; Task 1 tests cross-platform identity and unchanged manifest.
3. The aggregate checksum path aliases a library or an archive: reject before the existing manifest is removed; Task 1 tests the manifest alias and retains the alias.
4. An absent-platform stale zip aliases an existing input: reject before stale cleanup; Task 1's all-platform preflight and regression cover it.
5. The FreeBSD base archive changes or the compiler produces a non-FreeBSD artifact: abort before package/upload; Task 2 tests SHA verification and asserts the generated ELF type and machine in CI.

---

### Task 1: Reject aggregate package path aliases

**Files:**
- Modify: `.github/scripts/package-release_test.go`, near the aggregate tests starting at line 542.
- Modify: `.github/scripts/package-release.go:233-287`.

**Interfaces:**
- Consumes: `packageExistingArtifacts(version, distDir, outDir string) error`, `artifactSpecs() []artifactSpec`, `artifactSpec.binaryPath(distDir string) string`, `validateDistinctPaths(paths ...string) error`.
- Produces: the same public script flags and package layout; aggregate preflight rejects input/output aliases before `os.MkdirAll`, `removeChecksum`, `packageLibrary`, or stale-zip cleanup.

- [ ] **Step 1: Add every failing alias regression before implementation.** In a new `TestPackageExistingArtifactsRejectsAliasedArchivesBeforeWriting`, use `t.TempDir()` for each subtest to create `dist/linux_amd64/model-mapper.so` with literal `[]byte("original-plugin")` and `.version` with `[]byte("0.5.7\n")`. Create `out/checksums.txt` with `[]byte("old-manifest\n")`, then point `out/model-mapper_0.5.7_linux_amd64.zip` to the library using `os.Link` in one subtest and `os.Symlink` in another (`t.Skipf` only when that link type is unavailable). Add a two-input subtest (`linux_amd64`, `windows_amd64`) where the Linux output zip hardlinks to the Windows input, plus subtests where `checksums.txt` and an absent-platform stale zip alias an input. For every subtest assert `err` contains `must be distinct`, every input library retains its literal bytes, and prior outputs (including the manifest or alias path) remain untouched. The sentinel values come from fixtures, not the package writer.

```go
func TestPackageExistingArtifactsRejectsAliasedArchivesBeforeWriting(t *testing.T) {
    for _, tt := range []struct {
        name, output string
        targetWindows, symlink, staleWindows bool
    }{
        {name: "own hardlink", output: "linux"},
        {name: "own symlink", output: "linux", symlink: true},
        {name: "other platform input", output: "linux", targetWindows: true},
        {name: "manifest symlink", output: "manifest", symlink: true},
        {name: "stale archive symlink", output: "stale", symlink: true, staleWindows: true},
    } {
        t.Run(tt.name, func(t *testing.T) {
            dir := t.TempDir()
            dist, out := filepath.Join(dir, "dist"), filepath.Join(dir, "out")
            linux := filepath.Join(dist, "linux_amd64", "model-mapper.so")
            windows := filepath.Join(dist, "windows_amd64", "model-mapper.dll")
            inputs := []struct{ path, body string }{{linux, "original-linux"}}
            if !tt.staleWindows {
                inputs = append(inputs, struct{ path, body string }{windows, "original-windows"})
            }
            for _, input := range inputs {
                if err := os.MkdirAll(filepath.Dir(input.path), 0o755); err != nil { t.Fatal(err) }
                if err := os.WriteFile(input.path, []byte(input.body), 0o644); err != nil { t.Fatal(err) }
                if err := os.WriteFile(input.path+".version", []byte("0.5.7\n"), 0o644); err != nil { t.Fatal(err) }
            }
            if err := os.MkdirAll(out, 0o755); err != nil { t.Fatal(err) }
            manifest := filepath.Join(out, "checksums.txt")
            archive := filepath.Join(out, "model-mapper_0.5.7_linux_amd64.zip")
            switch tt.output {
            case "manifest": archive = manifest
            case "stale": archive = filepath.Join(out, "model-mapper_0.5.7_windows_amd64.zip")
            }
            if archive != manifest {
                if err := os.WriteFile(manifest, []byte("old-manifest\n"), 0o644); err != nil { t.Fatal(err) }
            }
            target := linux
            if tt.targetWindows { target = windows }
            link := os.Link
            if tt.symlink { link = os.Symlink }
            if err := link(target, archive); err != nil { t.Skipf("link unavailable: %v", err) }

            err := packageExistingArtifacts("0.5.7", dist, out)
            if err == nil || !strings.Contains(err.Error(), "must be distinct") {
                t.Fatalf("packageExistingArtifacts error = %v, want path alias rejection", err)
            }
            for _, input := range inputs {
                if got, err := os.ReadFile(input.path); err != nil || string(got) != input.body {
                    t.Fatalf("library %s after rejected package = %q, %v", input.path, got, err)
                }
            }
            targetInfo, err := os.Stat(target)
            if err != nil { t.Fatal(err) }
            aliasInfo, err := os.Stat(archive)
            if err != nil || !os.SameFile(targetInfo, aliasInfo) {
                t.Fatalf("alias no longer points to input: %v", err)
            }
            if tt.symlink {
                linkInfo, err := os.Lstat(archive)
                if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
                    t.Fatalf("archive symlink replaced: %v", err)
                }
                if linked, err := os.Readlink(archive); err != nil || linked != target {
                    t.Fatalf("archive symlink target = %q, %v; want %q", linked, err, target)
                }
            }
            if archive != manifest {
                if got, err := os.ReadFile(manifest); err != nil || string(got) != "old-manifest\n" {
                    t.Fatalf("manifest after rejected package = %q, %v", got, err)
                }
            }
        })
    }
}
```

- [ ] **Step 2: Watch every alias case fail for the correct reason.** Run `go test .github/scripts/package-release.go .github/scripts/package-release_test.go -run '^TestPackageExistingArtifactsRejectsAliasedArchivesBeforeWriting$' -count=1 -v` and inspect each subtest; the old implementation must succeed or mutate a prior file, not fail merely due to fixture setup. If Windows skips symlink creation, run the same focused test on the existing Debian WSL environment from the worktree's `/mnt/c/...` path before changing production code, and record the symlink subtests' behavioral failures. A skipped subtest is not a witnessed TDD red result.

- [ ] **Step 3: Add the global preflight before the first output mutation.** Just after the `len(artifacts)==0` guard and before `os.MkdirAll(outDir,...)`, collect every discovered `artifact.binaryPath(distDir)`, every platform's current-version zip path from `artifactSpecs()` (including absent platforms that stale cleanup may remove), and `filepath.Join(outDir, "checksums.txt")`. Call `validateDistinctPaths(paths...)` once and return its error. Keep the packaging loop and existing `removeChecksum` behavior untouched.

```go
paths := make([]string, 0, len(artifacts)+len(artifactSpecs())+1)
for _, artifact := range artifacts {
    paths = append(paths, artifact.binaryPath(distDir))
}
for _, artifact := range artifactSpecs() {
    zipName := fmt.Sprintf("%s_%s_%s_%s.zip", pluginName, version, artifact.osName, artifact.arch)
    paths = append(paths, filepath.Join(outDir, zipName))
}
paths = append(paths, filepath.Join(outDir, "checksums.txt"))
if err := validateDistinctPaths(paths...); err != nil {
    return err
}
```

- [ ] **Step 4: Verify all red cases turn green.** Re-run the focused alias subtests and the full explicit script test; verify normal aggregate and single-platform packaging tests still pass. If a newly added case had only a fixture/setup error in Step 2, correct that case and witness its behavior failure on the old implementation before calling it green.

- [ ] **Step 5: Verify and commit the independent deliverable.** Run `go test .github/scripts/package-release.go .github/scripts/package-release_test.go -count=1`, `go test ./...`, `go vet ./...`, and `git diff --check`. Commit only the two package-release files with message `fix: reject aggregate package aliases before writing`. In an isolated coding worktree, report the commit SHA for integration; do not push or tag.

### Task 2: Restore the FreeBSD/amd64 cross-build

**Files:**
- Modify: `.github/workflows/build.yml:236-248` only (plus a directly related test if a *behavioral* script test proves necessary; do not add a source-text grep test).

**Interfaces:**
- Consumes: existing `VERSION`, `PLUGIN_NAME`, `ARCHIVE_NAME`, Ubuntu 24.04 runner, Go 1.26 and Clang/lld available on that runner.
- Produces: `dist/freebsd_amd64/model-mapper.so`, consumed by the unchanged `Package plugin` and `Upload build artifact` steps; seven-platform `release.needs` remains unchanged.

- [ ] **Step 1: Record the observed red integration result.** The previous main [Build run 36438682239](https://github.com/DoingDog/cpa-plugin-model-mapper/actions/runs/36438682239/job/108984038751) fails inside `go-cross/cgo-actions@v1` at `wget -q .../14.3-RELEASE/base.txz` with exit 8. Its source and bundle hardcode 14.3, and an independent request now returns 404. Do not fabricate a `with:` override or treat the prior successful test job as a green build.

- [ ] **Step 2: Replace the action with a pinned, verified shell build.** Retain the step name and timeout. Use `run: |`, `set -euo pipefail`, `curl --fail --location --show-error --silent` for the official 14.4 archive, `sha256sum --check --status` before `tar -xf`, and `CC="clang --target=x86_64-unknown-freebsd14.4 --sysroot=${sysroot}"` with `CGO_LDFLAGS=-fuse-ld=lld` for the Go build. Preserve all old build flags and version injection.

```bash
set -euo pipefail
archive="${RUNNER_TEMP}/freebsd-14.4-base.txz"
sysroot="${RUNNER_TEMP}/freebsd-14.4-amd64"
curl --fail --location --show-error --silent \
  'https://download.freebsd.org/releases/amd64/amd64/14.4-RELEASE/base.txz' -o "${archive}"
printf '%s  %s\n' '769f60a6eea2938ad6b7943cfbcc17dfaf7d1f7ba59a32b869a00349302df853' "${archive}" | sha256sum --check --status
mkdir -p "${sysroot}" dist/freebsd_amd64
tar -xf "${archive}" -C "${sysroot}"
CGO_ENABLED=1 GOOS=freebsd GOARCH=amd64 \
  CC="clang --target=x86_64-unknown-freebsd14.4 --sysroot=${sysroot}" \
  CGO_LDFLAGS=-fuse-ld=lld \
  go build -trimpath -buildmode=c-shared \
    -ldflags="-s -w -X main.pluginVersion=${VERSION}" \
    -o "dist/freebsd_amd64/${PLUGIN_NAME}.so" .
```

- [ ] **Step 3: Verify output format and failure handling.** On a Linux runner with the required compiler, execute the exact shell block and require a nonempty `.so`; `file` and `readelf -h` must identify an ELF shared object with x86-64 machine and `DYN` type. Test checksum failure using a small temporary dummy archive and a deliberately wrong expected digest, asserting the shell stops before extraction/build; do not replace the real pinned digest to make this test pass. In CI, the FreeBSD job must pass package/upload and the zip must contain the nonempty `.so`; compare its `.sha256` line to the downloaded zip. A local Windows test alone cannot validate the cross-link.

- [ ] **Step 4: Verify and commit the independent deliverable.** Run existing explicit release script tests, `git diff --check`, and whichever local Linux cross-build is available. Commit only `.github/workflows/build.yml` with message `fix: pin supported FreeBSD sysroot for CI`. In an isolated coding worktree, report the commit SHA for integration; do not push or tag.

### Task 3: Integrate, verify, and publish only on green CI

**Files:**
- Create and commit: this task's spec and plan files if they were not committed before Task 1/2.
- No additional source edits unless verification exposes a reproducible failure; any fix gets its own red/green test first and a new commit.

**Interfaces:**
- Consumes: both independent task commits with disjoint files.
- Produces: a clean local `main`, the next patch tag `v0.5.7`, and complete green main/tag GitHub Actions runs with release assets.

- [ ] **Step 1: Integrate both commits into the audit worktree.** Cherry-pick only reported task commits (if isolated), inspect `git diff 8bda61d..HEAD --check`, ensure no old plans/specs were read or modified, and run `git status --short` to catch unintended files.
- [ ] **Step 2: Run the complete local suite without skipped integration.** Set a native Windows `TMPDIR` if necessary, then run `go test ./...`, `go test .github/scripts/package-release.go .github/scripts/package-release_test.go`, `go test .github/scripts/check-release-compatibility.go .github/scripts/check-release-compatibility_test.go`, `go test .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go`, `go vet ./...`, `go test -race .github/scripts/smoke-local.go .github/scripts/smoke-local_test.go`, and `make integration` (must print `=== RUN TestCPAPluginIntegration` and PASS). Run the real CPA integration test with `-race` using `CPA_SMOKE_INTEGRATION=1`, `CPA_SMOKE_CPA_BIN=<built binary>`, and `CPA_SMOKE_PLUGIN=<freshly built library>` as in the Makefile. These verify registration/effective enablement and an actual mapped request, not every format.
- [ ] **Step 3: Review against the spec and recheck remote state.** Read only this task's spec/plan and the changed diff; verify symlink/hardlink tests actually failed on the old code, SHA matches the official MANIFEST, archive contents and checksum match, and cross-RPC limitations remain explicitly unclaimed. Recheck `origin/main`, working trees, remote tags and latest release; abort fast-forward or tag if another push has changed the base or tag already exists.
- [ ] **Step 4: Fast-forward local main and push it.** From the audit worktree, fast-forward the tracked, clean original checkout's local `main` to the verified audit commit without touching its pre-existing untracked `.claude/`; push `main` (no force). Watch the resulting GitHub Actions run to terminal status: all tests and all seven build platforms must pass. Inspect the FreeBSD artifact zip and checksum. Do not proceed on a failing or pending required job.
- [ ] **Step 5: Publish the patch tag and verify release.** Create annotated `v0.5.7` on the verified commit, push only that tag, wait for its tag workflow to finish successfully, and verify the published release includes seven platform zips and `checksums.txt`; inspect that FreeBSD zip contains nonempty `model-mapper.so` and checksum matches. If the tag workflow fails, diagnose and repair before claiming release success; do not erase a published tag or force-push it.
- [ ] **Step 6: Report exact outcomes.** State the two repaired defects, the passing local/CI checks, commit and release links, and the cross-RPC/stream/performance limitations recorded in the spec without claiming they were fixed or that FreeBSD runtime loading was tested. Remove the temporary `current-cpa-functional-audit.md` memory and its `MEMORY.md` index entry only after a terminal result is recorded.
