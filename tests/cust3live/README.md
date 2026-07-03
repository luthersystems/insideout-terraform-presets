# cust3live — env-gated LIVE terraform tests

`tests/cust3live/live_test.go` (build tag `cust3live`) exercises the composer's
generated stacks against a **real** `terraform` binary and **real** AWS in the
cust3 TEST account (`031780745048`), so any agent/dev with the creds can
validate a composer change against real terraform + real AWS in ~1 minute.

## Plan-only by design

The checked-in tests **never** run `terraform apply` or `terraform destroy`:

- `TestCust3Live_ComposedStackPlans` — composes an `aws_s3` stack, then
  `terraform init` + `terraform plan` (a real STS assume + AWS API round trip).
  Asserts `Plan: N to add, 0 to change, 0 to destroy`.
- `TestCust3Live_ImportOnlyStackValidates` — composes an import-only stack with
  a synthetic `Imported` fixture (an `aws_route_table` + an `aws_s3_bucket`),
  then `terraform init` + `terraform validate` against the real pinned provider.
  Catches the `odb_network_arn` class (schema-level rejection of emitted
  literals). Fake import IDs are fine — validate never contacts AWS for them; a
  plan would fail on the fakes, which is why this test stops at validate.

The full apply → destroy loop stays **manual** via `cmd/composetest` (compose to
a dir, then drive `terraform init/plan/apply/destroy` by hand).

## Credentials

Creds resolve from 1Password: item `claude-web-tf-cust3`, vault `Reliable-Dev`.
The IAM user has exactly one permission — `sts:AssumeRole` into
`arn:aws:iam::031780745048:role/claude-web-tf-session` (admin in the cust3 TEST
account, **deny-guardrailed**). The provider blocks the composer emits assume
that role via the `bootstrap_role` variable.

## Run

```bash
# env file has AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY as op:// references
op run --env-file=<env-file-with-op-refs> -- \
  go test -tags cust3live ./tests/cust3live/ -v
```

Without creds (or without `terraform` on PATH) both tests **skip** with the
runbook message. Without the `cust3live` tag the package is not built at all, so
normal CI (`go test ./...`) is unaffected.

## Overrides

- `CUST3_SESSION_ROLE_ARN` — role ARN to assume (default
  `arn:aws:iam::031780745048:role/claude-web-tf-session`).

## Gotcha

The gate scrubs `AWS_SESSION_TOKEN` / `AWS_SECURITY_TOKEN` / `AWS_PROFILE`
before use: a stale session token from a prior assume-role silently breaks the
bare cust3 access key with `InvalidClientTokenId`.
