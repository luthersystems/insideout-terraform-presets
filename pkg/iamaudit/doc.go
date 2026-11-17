// Package iamaudit records which AWS IAM actions a deploy role actually used,
// from CloudTrail, and diffs them against the deploy policy the role will be
// narrowed to.
//
// It is the tool for Phase 1 of the least-privilege deploy role
// (luthersystems/sandbox-infrastructure-template#147, design doc
// docs/design/least-privilege-deploy-role.md §8.2 there). The customer-account
// deploy roles (bootstrap_role and the durable terraform_role) hold an
// admin-equivalent InsideOutWrite policy since Phase 0. Before Phase 2 narrows
// that policy to the generated allowlist, every action a real deploy uses must
// be in the allowlist, or the narrowing breaks deploys mid-apply (the
// reliable#2243 failure class). This package measures that:
//
//   - used but not allowed: must be empty before Phase 2;
//   - allowed but never used: candidates to drop from the allowlist;
//   - services used beyond the composer's create-only seed
//     (composer.AWSIAMActions): evidence for the generator's transitive
//     service set.
//
// CloudTrail names an API call by its event source and event name, which are
// not always the IAM action that authorizes the call. MapEvent turns one into
// the other and documents each known mismatch. Some actions are authorized
// without an event of their own (iam:PassRole most of all); the report keeps
// those out of the drop candidates rather than call them unused.
package iamaudit
