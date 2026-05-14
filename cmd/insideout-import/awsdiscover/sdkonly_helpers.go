package awsdiscover

import (
	"errors"

	smithy "github.com/aws/smithy-go"
)

// isAPIErrorCode reports whether err unwraps to a smithy.APIError whose
// ErrorCode matches any of codes. Cross-service helper shared by the
// SDK-only sub-resource discoverers (Bundle 14k1+14k2) — DDB, IAM,
// WAFv2, and AutoScaling all surface "this child does not exist" via
// typed-error or generic-API-error codes (ResourceNotFoundException,
// NoSuchEntity, WAFInvalidParameterException, …) that the per-type
// FetchItem / FetchItems closures convert into "skip silently."
//
// The S3 sub-resources (14k1) use a similar helper (isS3NotSetError)
// kept in sdkonly_s3.go for proximity to the per-RPC code lists; the
// implementation is identical. Both helpers are kept rather than one
// being deleted because the doc comments on isS3NotSetError pin the
// S3-specific code set (NoSuchLifecycleConfiguration etc.) and serve
// as in-tree reference for those code names.
//
// nil err returns false; non-smithy errors return false.
func isAPIErrorCode(err error, codes ...string) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		got := apiErr.ErrorCode()
		for _, want := range codes {
			if got == want {
				return true
			}
		}
	}
	return false
}
