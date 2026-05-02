// tflint configuration for insideout-terraform-presets.
//
// CI runs `tflint --init && tflint --recursive` from the repo root via the
// `tflint` job in .github/workflows/terraform-validate.yml. This file is
// the authority for which rules apply and at what severity.
//
// Coexistence with the custom shell lints under tests/lint-*.sh: tflint
// covers generic Terraform / provider correctness (deprecated syntax,
// unknown attributes, version-pin smells); the shell lints cover repo
// invariants tflint doesn't model (project-tag/label coverage, root-only
// blocks #199, sensitive-in-for_each, phantom-computed-fields denylist).
// Disable a tflint rule here only when an in-repo shell lint already
// enforces an equivalent or stricter check.

config {
  // Recurse into all Terraform working dirs (every aws/* and gcp/*
  // preset, plus examples/*) when invoked with --recursive. Without this
  // tflint only sees the directory it was invoked from.
  call_module_type = "all"
}

plugin "terraform" {
  enabled = true
  preset  = "recommended"
}

plugin "google" {
  enabled = true
  version = "0.31.0"
  source  = "github.com/terraform-linters/tflint-ruleset-google"
}

plugin "aws" {
  enabled = true
  version = "0.39.0"
  source  = "github.com/terraform-linters/tflint-ruleset-aws"
}

// terraform_unused_declarations stays enabled — composer-mandated vars
// (var.project / var.region / var.environment) that the module body
// doesn't reference are annotated with `# tflint-ignore:
// terraform_unused_declarations` directly above the variable block, with
// a comment explaining why. Genuinely unused declarations still fail.
