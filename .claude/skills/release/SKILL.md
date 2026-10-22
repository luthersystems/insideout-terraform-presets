# /release — Tag and Release Skill

Create a semver-tagged release for the Go module.

## Trigger

Use when asked to cut a release, tag a version, create a release, or publish a new version.

## Workflow

### 1. Determine Version

Check the latest existing tag:

```bash
git tag --sort=-v:refname | head -5
```

If no tags exist, start with `v0.1.0`.

Follow semantic versioning:
- **Patch** (`v0.1.1`): bug fixes, formatting, documentation
- **Minor** (`v0.2.0`): new modules, new features, non-breaking enhancements
- **Major** (`v1.0.0` / `v2.0.0`): breaking changes (variable renames, removed outputs, changed defaults)

### 2. Review Changes Since Last Release

```bash
git log $(git describe --tags --abbrev=0 2>/dev/null || echo "HEAD~20")..HEAD --oneline
```

Classify each commit to determine the appropriate version bump.

### 3. Verify

Follow the `/verify` skill to ensure everything passes before tagging.

### 4. Verify Go Module

Ensure the Go embed compiles cleanly:

```bash
go build ./...
```

### 5. Cut the Release

Dispatch the `Release (tag)` workflow (`.github/workflows/release-tag.yml`) on
`main`. Do not run `git push origin v<X.Y.Z>`: agent sandboxes cannot push tags
or create releases, and the workflow checks what a hand-cut tag skips (the
commit is live `main`, and Terraform Validate passed on its push run).

Run the dry run, wait for it, and read the version it printed before the real
run. `gh workflow run` returns before the run exists, so look up each run's ID
by the time you dispatched it, never with a bare `-L1`, which can return an
older run (the dry run, or someone else's).

```bash
# watch_new_run: wait for the release-tag run created after $1, then watch it.
watch_new_run() {
  local since=$1 id=""
  for _ in $(seq 30); do
    id=$(gh run list -w release-tag.yml -e workflow_dispatch -L 5 \
      --json databaseId,createdAt \
      --jq "[.[] | select(.createdAt >= \"$since\")] | last | .databaseId // empty")
    [ -n "$id" ] && break
    sleep 2
  done
  [ -n "$id" ] || { echo "no new release-tag run found" >&2; return 1; }
  gh run watch "$id" --exit-status && gh run view "$id" --log | grep -E 'DRY RUN|released|error:'
}

# 1. Dry run: prints the version it would cut and runs every check
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
gh workflow run release-tag.yml --ref main -f version=v<X.Y.Z> -f dry_run=true
watch_new_run "$since"

# 2. Only when the dry run printed the right version: the real run
#    (empty version = next patch of the latest tag)
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
gh workflow run release-tag.yml --ref main -f version=v<X.Y.Z> -f dry_run=false
watch_new_run "$since"
```

The workflow creates the tag and the GitHub release, with notes generated from
the merged pull requests. Then edit the release (`gh release edit v<X.Y.Z>
--notes-file ...`) and add a **Module Changes** section: new, changed and
removed modules, and any renamed variable or removed output. The downstream
InsideOut backend reads it when it bumps this module, and PR titles alone do
not show a breaking variable rename.

### 6. Verify Downstream Consumption

After release, the downstream InsideOut backend repo can update its `go.mod`:

```bash
go get github.com/luthersystems/insideout-terraform-presets@v<X.Y.Z>
```

Remind the user to update the downstream dependency.

## Version Decision Guide

| Change Type | Examples | Bump |
|---|---|---|
| Bug fix in existing module | Fix validation, wire variable | Patch |
| New module added | `aws/newservice`, `gcp/new_service` | Minor |
| New example added | `examples/newstack` | Minor |
| CI/tooling changes | Workflow updates, Claude skills | Patch |
| Variable renamed | `instance_type` → `node_type` | Major |
| Output removed | Removed `vpc_id` output | Major |
| Default changed (breaking) | Changed default from `null` to `"value"` | Major |

## Checklist

- [ ] Latest tag identified (or starting from v0.1.0)
- [ ] Changes reviewed and version bump determined
- [ ] `/verify` passes
- [ ] `go build ./...` succeeds
- [ ] Dry run printed the right version
- [ ] `Release (tag)` workflow run succeeded (tag and GitHub release created)
- [ ] Release notes have a Module Changes section
- [ ] User reminded to update downstream `go.mod`
