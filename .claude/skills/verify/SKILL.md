# /verify — Local CI Gate Skill

Run the same validation pipeline that GitHub Actions runs, locally.

## Trigger

Use when asked to verify, validate, check, or run CI locally — or before creating a PR.

## Workflow

### 1. Format Check

Run the Terraform format check across the entire repo:

```bash
terraform fmt -check -recursive
```

If formatting issues are found, fix them:

```bash
terraform fmt -recursive
```

### 1.5. Static Lints

Run the repo's static analysis scripts. These catch HCL-level patterns that
`terraform validate` cannot:

```bash
bash tests/lint-project-tag.sh
bash tests/lint-project-label.sh
bash tests/lint-sensitive-for-each.sh
bash tests/lint-foreach-unknown-keys.sh
```

Each must print `PASS`. Fail the whole verification if any prints `FAIL`.

### 2. Discover Modules

Find all preset modules, respecting `.validate-skip` markers:

```bash
find aws gcp -mindepth 1 -maxdepth 1 -type d | sort | while read -r d; do
  [ -f "$d/.validate-skip" ] && echo "SKIP: $d" && continue
  echo "$d"
done
```

### 3. Validate Preset Modules

For each discovered module (not skipped), run init + validate:

```bash
cd <module-dir> && terraform init -backend=false -input=false && terraform validate
```

Run modules in parallel where possible. On failure, record the module and error but continue validating remaining modules.

### 4. Discover and Validate Examples

Find and validate all example stacks:

```bash
find examples -mindepth 1 -maxdepth 1 -type d | sort | while read -r d; do
  echo "Validating $d..."
  (cd "$d" && terraform init -backend=false -input=false && terraform validate)
done
```

### 5. Report Results

Summarize: total modules checked, skipped, passed, failed. List any failures with their error messages.

## Quick Mode

If only a single module was changed, validate just that module and any examples that reference it:

```bash
cd <module-dir> && terraform init -backend=false -input=false && terraform validate
grep -rl '<module-dir>' examples/*/main.tf | xargs -I{} dirname {} | sort -u
```

## Checklist

- [ ] `terraform fmt -check -recursive` passes (or issues fixed)
- [ ] All four static lint scripts pass (`lint-project-tag.sh`, `lint-project-label.sh`, `lint-sensitive-for-each.sh`, `lint-foreach-unknown-keys.sh`)
- [ ] All preset modules validate (excluding `.validate-skip`)
- [ ] All example stacks validate
- [ ] Results summarized to user
