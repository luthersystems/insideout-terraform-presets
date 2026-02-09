# /pr — Pull Request Creation Skill

Create a pull request targeting main with proper formatting and verification.

## Trigger

Use when asked to create a PR, open a pull request, submit changes for review, or ship changes.

## Workflow

### 1. Pre-flight Checks

Verify you're on a feature branch (not `main`):

```bash
git branch --show-current
```

If on `main`, stop and ask the user to create a feature branch first.

### 2. Run Verification

Follow the `/verify` skill to ensure all modules validate and formatting is clean. Fix any issues before proceeding.

### 3. Stage and Commit

If there are uncommitted changes, stage and commit them using conventional commit format:

- `feat:` — new module or feature
- `fix:` — bug fix
- `chore:` — maintenance, deps
- `ci:` — CI/CD changes
- `style:` — formatting only
- `docs:` — documentation

### 4. Determine Base Branch

The base branch is always `main`:

```bash
git fetch origin main
```

### 5. Rebase on Base

Rebase the feature branch onto the latest main:

```bash
git rebase origin/main
```

If conflicts arise, resolve them and continue.

### 6. Push

Push the branch to the remote:

```bash
git push -u origin HEAD
```

### 7. Create PR

Create the PR with a structured body:

```bash
gh pr create --title "<type>: <short description>" --body "$(cat <<'EOF'
## Summary
- <bullet points describing what changed and why>

## Test plan
- [ ] `terraform fmt -check -recursive` passes
- [ ] All preset modules validate
- [ ] All example stacks validate
- [ ] <additional manual checks if applicable>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

**Title guidelines:**
- Use conventional commit prefix: `feat:`, `fix:`, `chore:`, `ci:`, `style:`, `docs:`
- Keep under 70 characters
- Describe the outcome, not the process

**Body guidelines:**
- Summary bullets: what changed and why (not how)
- Reference issues with `Fixes #N` or `Closes #N` when applicable
- Test plan should list all verification steps performed

### 8. Verify CI

After PR creation, check that CI passes:

```bash
gh pr checks
```

If CI fails, fix the issues, push, and re-check.

## Checklist

- [ ] On a feature branch (not main)
- [ ] `/verify` passes
- [ ] All changes committed with conventional commit messages
- [ ] Rebased on latest main
- [ ] Pushed to remote
- [ ] PR created with Summary + Test plan
- [ ] CI checks passing
