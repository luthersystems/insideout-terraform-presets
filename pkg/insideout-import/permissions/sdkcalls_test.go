package permissions

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// awsdiscoverDir is the awsdiscover source directory, relative to this
// package. The test reads the Go source, not the compiled package,
// because the SDK calls it checks sit inside unexported fetch helpers.
const awsdiscoverDir = "../../../cmd/insideout-import/awsdiscover"

// sdkCallSourceFiles lists the awsdiscover files whose AWS SDK calls
// this test checks, and the manifest service slug each file's calls
// belong to.
//
// Why these files: since #932 the multi-call enrichers stamp a resource
// Partial when a secondary call fails. A call whose IAM action is not
// in aws.json is never granted by a credential built from the
// manifest, so every such resource would come back Partial, with no
// hint that the manifest is the cause. That happened: the S3 bucket
// enricher called GetBucketLogging, GetBucketCors, GetBucketWebsite and
// GetObjectLockConfiguration, and the DynamoDB enricher called
// DescribeContinuousBackups and DescribeTimeToLive, with none of them
// in aws.json. Add a file here when a new enricher or discoverer makes
// its own SDK calls.
var sdkCallSourceFiles = map[string]string{
	"s3_bucket_enrich.go":            "s3",
	"s3_bucket_post_discover.go":     "s3",
	"dynamodb_table_enrich.go":       "dynamodb",
	"dynamodb_table_fetchers.gen.go": "dynamodb",
	"lambda_function_enrich.go":      "lambda",
}

// sdkOperationIAMAction maps an SDK operation to its IAM action where
// the two names differ. For most services the action is
// "<service>:<Operation>". S3 is the exception: many of its operations
// are authorized by an action with a different name, so every S3
// operation must be listed here (the test fails on an S3 operation it
// cannot find) rather than guessed.
var sdkOperationIAMAction = map[string]string{
	"s3.GetBucketCors":                   "s3:GetBucketCORS",
	"s3.GetBucketEncryption":             "s3:GetEncryptionConfiguration",
	"s3.GetBucketLifecycleConfiguration": "s3:GetLifecycleConfiguration",
	"s3.GetBucketLocation":               "s3:GetBucketLocation",
	"s3.GetBucketLogging":                "s3:GetBucketLogging",
	"s3.GetBucketPolicy":                 "s3:GetBucketPolicy",
	"s3.GetBucketTagging":                "s3:GetBucketTagging",
	"s3.GetBucketVersioning":             "s3:GetBucketVersioning",
	"s3.GetBucketWebsite":                "s3:GetBucketWebsite",
	"s3.GetObjectLockConfiguration":      "s3:GetBucketObjectLockConfiguration",
	// HeadBucket has no IAM action of its own; AWS authorizes it as
	// s3:ListBucket.
	"s3.HeadBucket": "s3:ListBucket",
}

// sdkCall is one AWS SDK operation found in a source file.
type sdkCall struct {
	pkg, op string
}

// findSDKCalls returns every SDK operation a file calls. It finds a call
// by its input argument, `&<pkg>.<Op>Input{...}`, which every AWS SDK v2
// operation takes and nothing else in these files builds.
func findSDKCalls(t *testing.T, path string) []sdkCall {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var calls []sdkCall
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || !strings.HasSuffix(sel.Sel.Name, "Input") {
			return true
		}
		calls = append(calls, sdkCall{pkg: pkg.Name, op: strings.TrimSuffix(sel.Sel.Name, "Input")})
		return true
	})
	return calls
}

// TestAWSManifest_CoversEnricherSDKCalls fails when an enricher or
// discoverer listed in sdkCallSourceFiles calls an AWS SDK operation
// whose IAM action is missing from aws.json under that file's service.
func TestAWSManifest_CoversEnricherSDKCalls(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}
	have := make(map[string]bool, len(m.Permissions))
	for _, p := range m.Permissions {
		have[p.Service+" "+p.IAMAction] = true
	}

	files := make([]string, 0, len(sdkCallSourceFiles))
	for f := range sdkCallSourceFiles {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		service := sdkCallSourceFiles[file]
		calls := findSDKCalls(t, filepath.Join(awsdiscoverDir, file))
		if len(calls) == 0 {
			t.Errorf("%s: no SDK calls found; the file moved or the call-finder broke, so this test would check nothing", file)
		}
		for _, c := range calls {
			action, ok := sdkOperationIAMAction[c.pkg+"."+c.op]
			if !ok {
				if c.pkg == "s3" {
					t.Errorf("%s: S3 operation %s has no entry in sdkOperationIAMAction; look up its IAM action and add it", file, c.op)
					continue
				}
				action = c.pkg + ":" + c.op
			}
			if !have[service+" "+action] {
				t.Errorf("aws.json: service %q is missing %q, which %s calls (%s.%s); add a row", service, action, file, c.pkg, c.op)
			}
		}
	}
}
