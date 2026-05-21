package imported

// LambdaCodeAttrs are the aws_lambda_function deployment-package
// attributes that an imported function cannot reproduce locally — the
// code lives in AWS, not on disk.
//
// The provider schema requires exactly one of filename / image_uri /
// s3_bucket, so the genconfig fixup injects a placeholder `filename` to
// make the generated block valid. Both that fixup and the composer's
// imported.tf emitter then pin every attribute below under
// `lifecycle { ignore_changes }` so `terraform apply` never (a) reads
// the nonexistent placeholder file, nor (b) re-uploads a placeholder
// over the live function's real code. Without the pin, importing a
// Lambda either fails the apply or destroys its code (#652).
//
// Declared here, in the shared IR package, so the genconfig fixup and
// the composer emitter pin the identical set — a drift between the two
// would reintroduce the bug.
var LambdaCodeAttrs = []string{
	"filename",
	"image_uri",
	"s3_bucket",
	"s3_key",
	"s3_object_version",
	"source_code_hash",
}

// LambdaPlaceholderFilename is the value pinned on
// `aws_lambda_function.filename` for an imported zip-package function
// whose deployment package cannot be reproduced locally (the code lives
// in AWS, not on disk, and lambda:GetFunction returns only a short-lived
// presigned URL — not the operator's original source).
//
// The provider's schema rule requires exactly one of filename /
// image_uri / s3_bucket; an imported zip function has none, so both the
// genconfig fixup (terraform-driven path) and the composer's imported.tf
// emitter (SDK-enrich path) inject this placeholder to satisfy the rule.
// It is always paired with `lifecycle { ignore_changes = LambdaCodeAttrs }`
// so `terraform apply` never reads the nonexistent file nor re-uploads
// the placeholder over the live function's real code (#652).
//
// terraform validate / plan does not open the file — that happens only
// at apply/build time, which the ignore_changes pin prevents — so the
// path may point at a file that does not exist. A neutral, unmistakable
// name keeps the generated stack self-documenting.
//
// Declared here, in the shared IR package, so the genconfig fixup and
// the composer emitter inject the identical value.
const LambdaPlaceholderFilename = "lambda_placeholder.zip"
