package depchase

// Literal-reference degradation catalogue (presets#834).
//
// The dep-chase loop's happy path RESOLVES a cross-resource ARN literal in the
// generated config to a real resource reference: it discovers the target, adds
// it to the import set, re-runs genconfig, and crossref.go rewrites the literal
// to `aws_<type>.<name>.<attr>`. A reference "degrades to a literal" when any
// stage below prevents that rewrite, so the ARN string is left verbatim in the
// generated config. All degradations are non-fatal — the stack still plans —
// but a literal weakens the imported graph (edits to the target don't
// propagate; later plans don't see the relationship).
//
// This catalogue enumerates every class that leaves a literal, keyed to the
// code that produces it, with a per-class decision: EMIT (resolve to a real
// reference — the happy path) or DOCUMENTED-LITERAL (the literal is the correct
// terminal state, or resolving it is out of scope for this repo).
//
// Reference types the loop can RESOLVE (arnTFTypeMap, arnparse.go): aws_iam_role,
// aws_iam_policy, aws_kms_key (from a kms key OR alias ARN), aws_s3_bucket,
// aws_lambda_function, aws_secretsmanager_secret, aws_dynamodb_table,
// aws_cloudwatch_log_group, aws_sqs_queue. Each is paired with a registered
// awsdiscover discoverer + a generated Layer-1 typed model, so a reference to a
// CUSTOMER-OWNED instance of one of these types is emitted and resolved.
//
//	# | Degradation class                     | Trigger (code)                                   | Warned?                              | Decision            | Rationale
//	--|---------------------------------------|--------------------------------------------------|--------------------------------------|---------------------|----------
//	A | Ref in a nested attr / block          | finder.collectFromBodyWithHits walks TOP-LEVEL   | no (silent)                          | documented-literal  | Conservative finder; nested ARN literals are rare. Widening is a follow-up (finder.go comment).
//	  |                                       | attrs only                                       |                                      | (widen finder)      |
//	B | Ref in a non-ARN identifier form      | finder.isARNLiteral requires `arn:` + >=5 colons | no (silent)                          | documented-literal  | Bare id/name/URL refs (KMS KeyId UUID, S3 name, GCP self-link) aren't ARN-shaped; out of the ARN-based chase's scope.
//	C | Unsupported ARN service/resource-type | ParseRef -> ErrUnsupportedType (not in           | yes: "unsupported ARN type"          | documented-literal  | Type has no discoverer + typed model (SNS, ECR, ACM, SES, EFS, ...). Emitting needs a new discoverer+model — deliberate scope expansion, not a chase bug.
//	  |                                       | arnTFTypeMap)                                    |                                      | (out of scope)      |
//	D | Malformed ARN literal                 | ParseRef -> parse error                          | yes: "could not parse ARN"           | documented-literal  | Not a resolvable reference.
//	E | Target not found                      | DiscoverByID -> Err{Not,}Found                    | yes: "ARN ... : not found"           | documented-literal  | Deleted / cross-account / cross-partition target; nothing to emit.
//	F | Discoverer rejected the ID            | DiscoverByID -> ErrNotSupported, incl.           | yes: "discoverer rejected ID"        | documented-literal  | Intended exclusion (e.g. AWS-managed IAM policy, ARN account="aws", #652). Not customer-owned.
//	  |                                       | SkipIdentifier exclusions                        |                                      | (intended)          |
//	G | Target inherently un-importable       | NEW gate: imported.UnimportableReason(ir) != ""  | yes: precise "un-importable (<reason>)" | documented-literal  | AWS-managed KMS key (KeyManager=AWS), service-linked IAM role (role/aws-service-role/), service-managed ENI, ManagedBy service resource, already-InsideOut-imported. The target CANNOT be adopted into customer TF state, so the literal is the correct terminal state. THIS is the #834 aws_kms_key / aws_iam_role class — now gated pre-add with a precise reason instead of the opaque class H warning.
//	H | Discovered, but genconfig dropped it  | partitionDiscoveries "dropped": address absent   | yes: "generated config omitted it;   | documented-literal  | Backstop for any un-adoptable target class G did not pre-classify (e.g. `terraform plan -generate-config-out` produced no body, or the genconfig unimportable-HCL prune removed it). Before #834 this was the ONLY signal for the KMS/IAM cases — opaque, read like a bug.
//	  |                                       | from regenerated set                             | leaving the literal reference"       | (backstop)          |
//	I | Reference cycle / stable unresolved   | unresolved set equal across iterations           | yes: "unresolved ... stable"         | documented-literal  | Target unreachable via DiscoverByID, or a reference cycle the loop can't close by adding resources.
//
// Decision summary: every SUPPORTED, CUSTOMER-OWNED target is EMITTED and its
// reference resolved (happy path). The classes above are all DOCUMENTED-LITERAL
// — either the target is genuinely un-adoptable (F, G), gone (E), or resolving
// it is out of the ARN-based chase's scope (A, B, C, D, I). #834's contribution
// is class G: move the two observed reference types (aws_kms_key, aws_iam_role)
// out of the opaque class-H backstop and into a precise, pre-add gate that
// names WHY the literal is correct, and add the missing service-linked-IAM-role
// classifier to the shared imported.UnimportableReason so discovery, the
// genconfig prune, and this loop all agree.
