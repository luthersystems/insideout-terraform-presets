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

```bash
# Dry run first: prints the version it would cut
gh workflow run release-tag.yml --ref main -f version=v<X.Y.Z> -f dry_run=true
# Then the real run (empty version = next patch of the latest tag)
gh workflow run release-tag.yml --ref main -f version=v<X.Y.Z> -f dry_run=false
gh run watch "$(gh run list -w release-tag.yml -L1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

The workflow creates the tag and the GitHub release, with notes generated from
the merged pull requests. Edit the release notes afterwards to add the module
changes if they matter to a downstream reader.

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
- [ ] `Release (tag)` workflow run succeeded (tag and GitHub release created)
- [ ] User reminded to update downstream `go.mod`
