# /pickup-issue — Issue to PR Lifecycle Skill

Take a GitHub issue from assignment through to a merged PR.

## Trigger

Use when asked to pick up an issue, work on issue #N, fix a bug from an issue, or implement a feature from an issue.

## Workflow

### 1. Fetch the Issue

```bash
gh issue view <number>
```

Read the issue title, description, labels, and any linked discussion.

### 2. Classify the Change

| Type | Branch prefix | Commit prefix |
|------|--------------|---------------|
| Bug fix | `fix/` | `fix:` |
| New module | `feature/` | `feat:` |
| Enhancement | `feature/` | `feat:` |
| Refactor | `refactor/` | `refactor:` |
| CI/CD | `feature/` | `ci:` |
| Documentation | `feature/` | `docs:` |

### 3. Create Branch

```bash
git checkout main && git pull origin main
git checkout -b <prefix><short-description>
```

Use a descriptive kebab-case name derived from the issue (e.g., `fix/ec2-capacity-type-validation`).

### 4. Investigate

Read the relevant module files to understand the current state:

- `<module>/main.tf` — resource definitions
- `<module>/variables.tf` — variable declarations and validations
- `<module>/outputs.tf` — output declarations

For bug fixes, identify root cause before writing code.

### 5. Implement

Make the code changes. Follow the relevant skill for the type of change:

- Adding a module: follow `/add-aws-module` or `/add-gcp-module`
- Adding an example: follow `/add-example`
- Modifying a module: apply the fix directly, respecting all conventions in CLAUDE.md

**Breaking change awareness:**
- Renaming a variable is breaking (downstream composer namespaces them)
- Adding a variable without a default is breaking (becomes required root variable)
- Removing an output is breaking (may be used for cross-module wiring)

### 6. Verify

Follow the `/verify` skill. At minimum, validate the changed module(s) and any examples that reference them.

### 7. Commit

Use conventional commit format referencing the issue:

```bash
git add <files>
git commit -m "$(cat <<'EOF'
<type>: <description>

<optional body explaining why>

Fixes #<N>

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

### 8. Create PR

Follow the `/pr` skill. Ensure the PR body includes `Fixes #<N>` or `Closes #<N>` to auto-close the issue on merge.

## Checklist

- [ ] Issue fetched and understood
- [ ] Branch created with correct prefix
- [ ] Root cause identified (for bugs)
- [ ] Changes implemented following project conventions
- [ ] No unintended breaking changes
- [ ] `/verify` passes
- [ ] Committed with conventional commit and issue reference
- [ ] PR created with `Fixes #N` / `Closes #N`
- [ ] CI passing
