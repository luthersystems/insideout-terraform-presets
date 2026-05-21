//go:build integration

// End-to-end confirmation for the policy-document attribute enrichers
// (#661 + follow-up): aws_iam_policy, aws_iam_role, aws_iam_role_policy,
// aws_s3_bucket_policy and the aws_lambda_function code overlay.
//
// Self-contained harness: the test provisions throwaway IAM / S3 /
// Lambda resources in the live account via the AWS SDK, runs the real
// registered enrichers through AWSDiscoverer.EnrichAttributes (the same
// dispatch the discover command uses), asserts every required policy-
// document attribute is populated with valid JSON, then tears every
// resource down via t.Cleanup — so a passing run leaves no residue and
// a mid-run failure still cleans up.
//
// Provisioning uses the AWS SDK rather than a terraform-exec wrapper:
// it needs no `terraform` binary or provider download inside the test,
// gives deterministic Cleanup-based teardown, and exercises exactly
// what #661 is about — real cloud resources fed through the real
// enrichers. The decision-#34 `terraform plan` zero-diff assertion
// stays in the downstream reliable integration suite (which owns the
// composer.EmitImportedTF + terraform rig), as noted in
// dynamodb_table_enrich_live_test.go.
//
// Run (from a shell with AWS creds loaded, e.g. aws_jump <acct> <role>):
//
//	go test -tags=integration ./cmd/insideout-import/awsdiscover/... \
//	    -v -run TestE2E661_PolicyDocumentEnrichers -timeout 15m
//
// Skips (not fails) when AWS credentials cannot be resolved so a
// no-creds CI invocation is a no-op.

package awsdiscover

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// e2eTrustPolicy is the role trust policy (lambda service principal so
// the same role can back the test Lambda function).
const e2eTrustPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

// e2ePermissionPolicy is the document used for both the managed policy
// and the inline role policy.
const e2ePermissionPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["logs:CreateLogStream","logs:PutLogEvents"],"Resource":"*"}]}`

// TestE2E661_PolicyDocumentEnrichers is the end-to-end confirmation.
func TestE2E661_PolicyDocumentEnrichers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Skipf("AWS config not resolvable, skipping: %v", err)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	region := cfg.Region

	// Credentials precondition — skip (not fail) when creds are absent.
	if _, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		t.Skipf("STS GetCallerIdentity failed (no usable AWS creds), skipping: %v", err)
	}

	iamc := iam.NewFromConfig(cfg)
	s3c := s3.NewFromConfig(cfg)
	lambdac := lambda.NewFromConfig(cfg)

	// Unique, collision-proof naming for this run.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	roleName := "io-e2e661-role-" + suffix
	inlineName := "io-e2e661-inline-" + suffix
	managedName := "io-e2e661-managed-" + suffix
	bucketName := "io-e2e661-bucket-" + suffix
	fnName := "io-e2e661-fn-" + suffix

	// ---- Provision: IAM role -------------------------------------------
	createRoleOut, err := iamc.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(e2eTrustPolicy),
		Description:              aws.String("insideout 661 e2e - safe to delete"),
		MaxSessionDuration:       aws.Int32(3600),
		Tags:                     []iamtypes.Tag{{Key: aws.String("Project"), Value: aws.String("io-e2e661")}},
	})
	require.NoError(t, err, "CreateRole")
	roleARN := aws.ToString(createRoleOut.Role.Arn)
	t.Cleanup(func() {
		if _, err := iamc.DeleteRole(context.Background(), &iam.DeleteRoleInput{RoleName: aws.String(roleName)}); err != nil {
			t.Logf("cleanup DeleteRole(%s): %v", roleName, err)
		}
	})

	// ---- Provision: inline role policy ---------------------------------
	_, err = iamc.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String(inlineName),
		PolicyDocument: aws.String(e2ePermissionPolicy),
	})
	require.NoError(t, err, "PutRolePolicy")
	t.Cleanup(func() {
		if _, err := iamc.DeleteRolePolicy(context.Background(), &iam.DeleteRolePolicyInput{
			RoleName: aws.String(roleName), PolicyName: aws.String(inlineName),
		}); err != nil {
			t.Logf("cleanup DeleteRolePolicy(%s): %v", inlineName, err)
		}
	})

	// ---- Provision: managed policy -------------------------------------
	createPolicyOut, err := iamc.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyName:     aws.String(managedName),
		PolicyDocument: aws.String(e2ePermissionPolicy),
		Description:    aws.String("insideout 661 e2e - safe to delete"),
		Tags:           []iamtypes.Tag{{Key: aws.String("Project"), Value: aws.String("io-e2e661")}},
	})
	require.NoError(t, err, "CreatePolicy")
	managedARN := aws.ToString(createPolicyOut.Policy.Arn)
	t.Cleanup(func() {
		if _, err := iamc.DeletePolicy(context.Background(), &iam.DeletePolicyInput{PolicyArn: aws.String(managedARN)}); err != nil {
			t.Logf("cleanup DeletePolicy(%s): %v", managedARN, err)
		}
	})

	// ---- Provision: S3 bucket + bucket policy --------------------------
	require.NoError(t, e2eCreateBucket(ctx, s3c, bucketName, region), "CreateBucket")
	t.Cleanup(func() {
		if _, err := s3c.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(bucketName)}); err != nil {
			t.Logf("cleanup DeleteBucket(%s): %v", bucketName, err)
		}
	})
	bucketPolicy := fmt.Sprintf(
		`{"Version":"2012-10-17","Statement":[{"Sid":"DenyInsecureTransport","Effect":"Deny","Principal":"*","Action":"s3:*","Resource":["arn:aws:s3:::%s","arn:aws:s3:::%s/*"],"Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`,
		bucketName, bucketName)
	_, err = s3c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucketName),
		Policy: aws.String(bucketPolicy),
	})
	require.NoError(t, err, "PutBucketPolicy")
	// DeleteBucketPolicy is implied by DeleteBucket, but delete it
	// explicitly first so a DeleteBucket retry isn't blocked by it.
	t.Cleanup(func() {
		if _, err := s3c.DeleteBucketPolicy(context.Background(), &s3.DeleteBucketPolicyInput{Bucket: aws.String(bucketName)}); err != nil {
			t.Logf("cleanup DeleteBucketPolicy(%s): %v", bucketName, err)
		}
	})

	// ---- Provision: Lambda function (zip package) ----------------------
	// IAM role propagation is eventually consistent; CreateFunction
	// rejects an as-yet-unpropagated role with InvalidParameterValue.
	fnARN := e2eCreateLambda(ctx, t, lambdac, fnName, roleARN)
	if fnARN != "" {
		t.Cleanup(func() {
			if _, err := lambdac.DeleteFunction(context.Background(), &lambda.DeleteFunctionInput{FunctionName: aws.String(fnName)}); err != nil {
				t.Logf("cleanup DeleteFunction(%s): %v", fnName, err)
			}
		})
	}

	// ---- Enrich: run the real registered enrichers ---------------------
	clients := EnrichClients{
		IAM:          iamc,
		S3:           s3c,
		Lambda:       lambdac,
		CloudControl: cloudcontrol.NewFromConfig(cfg),
	}
	disc := NewAWSDiscoverer(cfg)

	// Identities shaped exactly as the Cloud Control discoverers stamp
	// them (see cloudControlTypeConfigs).
	// Cloud + Tier are set so the same slice composes via EmitImportedTF
	// in the terraform-plan subtest below (the discoverer normally
	// stamps these; here the resources are hand-built).
	irs := []imported.ImportedResource{
		{Tier: imported.TierImportedFlat, Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_iam_policy", Address: "aws_iam_policy.e2e",
			ImportID: managedARN, NameHint: managedName, Region: region,
			NativeIDs: map[string]string{"arn": managedARN},
		}},
		{Tier: imported.TierImportedFlat, Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_iam_role", Address: "aws_iam_role.e2e",
			ImportID: roleName, NameHint: roleName, Region: region,
		}},
		{Tier: imported.TierImportedFlat, Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_iam_role_policy", Address: "aws_iam_role_policy.e2e",
			ImportID: roleName + ":" + inlineName, NameHint: inlineName, Region: region,
			NativeIDs: map[string]string{"role_name": roleName, "policy_name": inlineName},
		}},
		{Tier: imported.TierImportedFlat, Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_s3_bucket_policy", Address: "aws_s3_bucket_policy.e2e",
			ImportID: bucketName, NameHint: bucketName, Region: region,
			NativeIDs: map[string]string{"bucket": bucketName},
		}},
	}
	if fnARN != "" {
		irs = append(irs, imported.ImportedResource{Tier: imported.TierImportedFlat, Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_lambda_function", Address: "aws_lambda_function.e2e",
			ImportID: fnName, NameHint: fnName, Region: region,
			NativeIDs: map[string]string{"arn": fnARN},
		}})
	}

	if err := disc.EnrichAttributes(ctx, irs, clients, nil); err != nil {
		t.Fatalf("EnrichAttributes returned error: %v", err)
	}

	byType := map[string]*imported.ImportedResource{}
	for i := range irs {
		byType[irs[i].Identity.Type] = &irs[i]
	}

	t.Run("aws_iam_policy/policy populated", func(t *testing.T) {
		ir := byType["aws_iam_policy"]
		requireEnrichedFull(t, ir)
		var got generated.AWSIAMPolicy
		require.NoError(t, json.Unmarshal(ir.Attrs, &got))
		requireJSONLiteral(t, got.Policy, "policy")
		require.NotNil(t, got.Name)
		assert.Equal(t, managedName, *got.Name.Literal)
	})

	t.Run("aws_iam_role/assume_role_policy populated", func(t *testing.T) {
		ir := byType["aws_iam_role"]
		requireEnrichedFull(t, ir)
		var got generated.AWSIAMRole
		require.NoError(t, json.Unmarshal(ir.Attrs, &got))
		requireJSONLiteral(t, got.AssumeRolePolicy, "assume_role_policy")
		require.NotNil(t, got.Name)
		assert.Equal(t, roleName, *got.Name.Literal)
	})

	t.Run("aws_iam_role_policy/policy populated", func(t *testing.T) {
		ir := byType["aws_iam_role_policy"]
		requireEnrichedFull(t, ir)
		var got generated.AWSIAMRolePolicy
		require.NoError(t, json.Unmarshal(ir.Attrs, &got))
		requireJSONLiteral(t, got.Policy, "policy")
		require.NotNil(t, got.Role)
		assert.Equal(t, roleName, *got.Role.Literal)
	})

	t.Run("aws_s3_bucket_policy/policy populated", func(t *testing.T) {
		ir := byType["aws_s3_bucket_policy"]
		requireEnrichedFull(t, ir)
		var got generated.AWSS3BucketPolicy
		require.NoError(t, json.Unmarshal(ir.Attrs, &got))
		requireJSONLiteral(t, got.Policy, "policy")
		require.NotNil(t, got.Bucket)
		assert.Equal(t, bucketName, *got.Bucket.Literal)
	})

	t.Run("aws_lambda_function/package_type populated", func(t *testing.T) {
		ir := byType["aws_lambda_function"]
		if ir == nil {
			t.Skip("lambda function was not provisioned (see provisioning log)")
		}
		requireEnrichedFull(t, ir)
		var got generated.AWSLambdaFunction
		require.NoError(t, json.Unmarshal(ir.Attrs, &got))
		// The composite enricher stamps the authoritative package_type
		// from lambda:GetFunction. Our test function is a zip package.
		require.NotNil(t, got.PackageType, "package_type must be set by the code overlay")
		assert.Equal(t, "Zip", *got.PackageType.Literal)
		// CC still mapped the bulk of the function.
		require.NotNil(t, got.FunctionName)
		assert.Equal(t, fnName, *got.FunctionName.Literal)
	})

	// The load-bearing confirmation: compose the enriched IRs to HCL and
	// run `terraform plan` against the live account. This is what would
	// "blow up on the next plan" if a required policy-document argument
	// were empty or the lambda block lacked a code source — a plan-time
	// error, not a compose-time one. A non-empty diff is expected and
	// fine; a plan *error* is the regression this guards against.
	t.Run("terraform plan accepts the composed imported.tf", func(t *testing.T) {
		tfBin, lookErr := exec.LookPath("terraform")
		if lookErr != nil {
			t.Skip("terraform binary not on PATH")
		}
		out, _ := composer.EmitImportedTF("aws", irs, composer.EmitImportedOpts{})
		require.NotEmpty(t, out, "EmitImportedTF produced no HCL")

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "imported.tf"), out, 0o600))
		providers := fmt.Sprintf(`terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}
provider "aws" {
  region = %q
}
provider "aws" {
  alias  = "imported"
  region = %q
}
`, region, region)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "providers.tf"), []byte(providers), 0o600))
		// terraform plan never opens the lambda placeholder file, but
		// write a real zip anyway so the directory is self-consistent.
		if zipBytes, zerr := e2eLambdaZip(); zerr == nil {
			_ = os.WriteFile(filepath.Join(dir, imported.LambdaPlaceholderFilename), zipBytes, 0o600)
		}

		runTF := func(args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, tfBin, args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
			b, err := cmd.CombinedOutput()
			return string(b), err
		}
		if o, err := runTF("init", "-input=false", "-no-color"); err != nil {
			t.Fatalf("terraform init failed: %v\n%s", err, o)
		}
		// -detailed-exitcode: 0 = no diff, 2 = non-empty diff, 1 = error.
		// A diff is expected (imported config rarely matches the live
		// resource byte-for-byte); an error is the #663 regression.
		o, err := runTF("plan", "-input=false", "-no-color", "-detailed-exitcode")
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
				t.Logf("terraform plan clean (exit 2 = non-empty diff, expected for import)")
				return
			}
			t.Fatalf("terraform plan ERRORED — the composed imported.tf is not plan-clean:\n%s", o)
		}
		t.Logf("terraform plan clean (exit 0 = no diff)")
	})
}

// requireEnrichedFull asserts the enricher succeeded for ir.
func requireEnrichedFull(t *testing.T, ir *imported.ImportedResource) {
	t.Helper()
	require.NotNil(t, ir, "resource missing from enriched set")
	require.Equalf(t, imported.EnrichmentStatusFull, ir.Identity.EnrichmentStatus,
		"%s enrichment not Full: status=%v errors=%v", ir.Identity.Type, ir.Identity.EnrichmentStatus, ir.Identity.EnrichErrors)
	require.NotEmpty(t, ir.Attrs, "%s Attrs empty", ir.Identity.Type)
}

// requireJSONLiteral asserts v holds a non-empty, valid-JSON literal.
func requireJSONLiteral(t *testing.T, v *generated.Value[string], field string) {
	t.Helper()
	require.NotNilf(t, v, "%s must be populated", field)
	require.NotNilf(t, v.Literal, "%s literal must be set", field)
	require.NotEmptyf(t, *v.Literal, "%s must be non-empty", field)
	require.Truef(t, json.Valid([]byte(*v.Literal)), "%s must be valid JSON, got %q", field, *v.Literal)
}

// e2eCreateBucket creates a bucket, handling the us-east-1 special case
// (no LocationConstraint allowed there).
func e2eCreateBucket(ctx context.Context, c *s3.Client, name, region string) error {
	in := &s3.CreateBucketInput{Bucket: aws.String(name)}
	if region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}
	_, err := c.CreateBucket(ctx, in)
	return err
}

// e2eCreateLambda creates a zip-package Lambda, retrying CreateFunction
// while the freshly-created IAM role is still propagating. Returns the
// function ARN, or "" (with a logged reason) if provisioning could not
// complete — the caller skips the lambda subtest in that case rather
// than failing the IAM/S3 confirmation.
func e2eCreateLambda(ctx context.Context, t *testing.T, c *lambda.Client, name, roleARN string) string {
	t.Helper()
	zipBytes, err := e2eLambdaZip()
	if err != nil {
		t.Logf("lambda provisioning skipped: build zip: %v", err)
		return ""
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		out, err := c.CreateFunction(ctx, &lambda.CreateFunctionInput{
			FunctionName: aws.String(name),
			Role:         aws.String(roleARN),
			Runtime:      lambdatypes.RuntimePython312,
			Handler:      aws.String("index.handler"),
			PackageType:  lambdatypes.PackageTypeZip,
			Code:         &lambdatypes.FunctionCode{ZipFile: zipBytes},
			Description:  aws.String("insideout 661 e2e - safe to delete"),
		})
		if err == nil {
			return aws.ToString(out.FunctionArn)
		}
		// InvalidParameterValueException: the role cannot yet be
		// assumed by Lambda (IAM eventual consistency). Retry.
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidParameterValueException" && time.Now().Before(deadline) {
			time.Sleep(8 * time.Second)
			continue
		}
		t.Logf("lambda provisioning skipped: CreateFunction: %v", err)
		return ""
	}
}

// e2eLambdaZip builds a minimal in-memory zip deployment package with a
// no-op python handler.
func e2eLambdaZip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("index.py")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte("def handler(event, context):\n    return {}\n")); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
