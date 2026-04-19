# /audit — Terraform Module Audit Skill

Systematic audit of Terraform modules for common issues, security gaps, and convention violations.

## Trigger

Use when asked to audit, review, check quality, or scan the codebase for issues.

## Workflow

### 1. Scope the Audit

Determine what to audit. By default, audit everything. Accept scoped variants:

- `audit aws` — AWS modules only
- `audit gcp` — GCP modules only
- `audit security` — security-focused scan
- `audit conventions` — convention compliance only
- `audit <module>` — single module deep-dive

### 2. Convention Compliance

For each module, check:

| Convention | AWS | GCP |
|---|---|---|
| Directory naming | camelCase | snake_case |
| Has `main.tf` | Required | Required |
| Has `variables.tf` | Required | Required |
| Has `outputs.tf` | Required | Required |
| Has `project` variable | Required | Required |
| Has `region` variable | Required | Required |
| Has `tags`/`labels` variable | `tags` | `labels` |
| Provider version | AWS >= 6.0, TF >= 1.5 | Google >= 5.0, TF >= 1.0 |

```bash
# Check all modules have required files
for dir in aws/*/; do
  for f in main.tf variables.tf outputs.tf; do
    [ -f "$dir$f" ] || echo "MISSING: $dir$f"
  done
done
```

### 3. Validation Safety

Scan for unsafe null validation patterns:

```bash
grep -rn 'var\.\w\+ == null ||' aws/ gcp/
grep -rn 'var\.\w\+ != null &&' aws/ gcp/
```

These should use the ternary pattern instead:
- `var.x == null ? true : condition` (not `var.x == null || condition`)

### 4. Security Audit

Check for common security issues:

```bash
# Public access enabled
grep -rn 'public_access\|publicly_accessible\|public = true' aws/ gcp/

# Encryption disabled
grep -rn 'encrypted\s*=\s*false\|encryption\s*=\s*false' aws/ gcp/

# Overly permissive IAM
grep -rn '"*"\|"\*"' aws/ gcp/ --include="*.tf"
```

### 5. Tagging Coverage

The downstream reliable3 inspector filters AWS resources by exact `Project = <project>` tag match. Resources without a `tags = merge(module.name.tags, var.tags)` block are invisible to drift detection and CloudWatch metrics. Scan for taggable resources that omit the convention:

```bash
# AWS — flag resource blocks missing `tags`
for f in aws/*/main.tf; do
  awk '
    /^resource "aws_/ { name=$0; has_tags=0; next }
    /^  tags[[:space:]]*=/ { has_tags=1 }
    /^}/ { if (name != "" && !has_tags) print FILENAME": "name; name=""; has_tags=0 }
  ' "$f"
done

# GCP — flag resource blocks missing `labels`
for f in gcp/*/main.tf; do
  awk '
    /^resource "google_/ { name=$0; has_labels=0; next }
    /^  labels[[:space:]]*=/ { has_labels=1 }
    /^}/ { if (name != "" && !has_labels) print FILENAME": "name; name=""; has_labels=0 }
  ' "$f"
done
```

The heuristic over-reports — some resources are genuinely untaggable (e.g. `aws_iam_role_policy_attachment`, `aws_iam_role_policy`, `aws_route53_record`, `aws_s3_bucket_public_access_block`). Hand-verify each hit against the provider docs before reporting a gap.

### 6. Region Reference Audit (AWS)

Check for direct `var.region` usage in service name construction (should use `data.aws_region.current.region`):

```bash
grep -rn 'var\.region' aws/ --include="*.tf" | grep -v 'variables.tf'
```

### 7. Cross-Module Wiring

Verify outputs match what examples expect:

```bash
# List all module output references in examples
grep -rn 'module\.\w\+\.\w\+' examples/ --include="*.tf"

# Cross-reference with actual outputs
for dir in aws/*/; do
  module=$(basename "$dir")
  grep -l "module\.$module\." examples/*/main.tf 2>/dev/null && \
    echo "--- $module outputs ---" && \
    grep 'output "' "$dir/outputs.tf"
done
```

### 8. Go Embed Coverage

Verify all file patterns are embedded:

```bash
# Check for file extensions not covered by embed directives
find aws gcp -type f | grep -v '\.tf$' | grep -v '\.tmpl$' | grep -v '\.terraform' | grep -v '.validate-skip'
```

### 9. Report

Produce a structured report:

```
## Audit Report

### Convention Violations
- <list>

### Security Issues
- <list>

### Validation Safety
- <list>

### Tagging Coverage
- <list — resources missing `tags`/`labels` per the Project-tag convention>

### Wiring Issues
- <list>

### Recommendations
- <prioritized list>
```

## Checklist

- [ ] Convention compliance checked (file structure, naming, required variables)
- [ ] Null validation patterns scanned
- [ ] Security defaults verified
- [ ] Tagging coverage verified (AWS `tags`, GCP `labels` on every taggable resource)
- [ ] Region references audited (AWS)
- [ ] Cross-module wiring validated
- [ ] Go embed coverage confirmed
- [ ] Report produced with findings and recommendations
