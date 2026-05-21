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
