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

### 5. Create the Tag

```bash
git tag -a v<X.Y.Z> -m "v<X.Y.Z>: <summary of changes>"
```

### 6. Push the Tag

```bash
git push origin v<X.Y.Z>
```

### 7. Create GitHub Release

```bash
gh release create v<X.Y.Z> --title "v<X.Y.Z>" --notes "$(cat <<'EOF'
## What's Changed

- <bullet points of notable changes>

## Module Changes

- <new/modified/removed modules>

**Full Changelog**: https://github.com/luthersystems/insideout-terraform-presets/compare/<prev-tag>...v<X.Y.Z>
EOF
)"
```

### 8. Verify Downstream Consumption

After release, the downstream `reliable` repo can update its `go.mod`:

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
- [ ] Tag created and pushed
- [ ] GitHub release created with changelog
- [ ] User reminded to update downstream `go.mod`
