//go:build integration

// Live probe for the "CloudControl cannot read back a required field"
// bug class (#665 — the class behind #661 / #662 / #664).
//
// The static tripwire (cc_required_field_tripwire_test.go) freezes the
// at-risk surface but cannot PROVE whether cloudcontrol:GetResource
// actually returns a given required field — the CFN registry schema
// does not declare the gap (AWS::IAM::ManagedPolicy does not mark
// PolicyDocument write-only, yet the handler omits it). Only a real
// GetResource against a real resource can.
//
// This probe provisions throwaway instances of the suspected types,
// then runs the genuine discover→enrich pipeline against them:
// DiscoverTypes produces the production-faithful Identity (so the probe
// can't accidentally test a strawman identifier), and EnrichAttributes
// routes each to its generic CloudControl enricher. It then asserts
// every REQUIRED schema field came back populated — an empty required
// field is the bug, confirmed for that type.
//
// expectedGaps records the types a prior run found broken: the probe
// downgrades a known gap to a logged warning (so the probe stays green
// while #665 tracks the fix) but FAILS if (a) a not-listed type turns
// up broken — a new instance of the class — or (b) a listed type turns
// up fixed and should be removed from the map.
//
// Run (from a shell with AWS creds, e.g. aws_jump <acct> <role>):
//
//	go test -tags=integration ./cmd/insideout-import/awsdiscover/... \
//	    -v -run TestLive665_CloudControlRequiredFieldProbe -timeout 15m
//
// Self-skips when AWS credentials cannot be resolved.

package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// expectedGaps lists types a prior probe run confirmed the CloudControl
// path cannot fully populate, each pointing at the tracking issue. A
// type in this map is a logged warning, not a failure; a type NOT in
// it that turns up broken fails the probe (new bug-class instance), and
// a listed type that turns up fixed also fails (stale entry — remove).
var expectedGaps = map[string]string{
	// aws_key_pair: cloudcontrol:GetResource for AWS::EC2::KeyPair
	// returns the fingerprint but never the `public_key` material —
	// EC2 does not expose it on read, so NO enricher can recover it.
	// This is a permanent enricher-layer gap by design; the #665 fix
	// is composer-layer (composer.ensureKeyPairPlaceholder injects a
	// placeholder public_key pinned under lifecycle.ignore_changes).
	// The probe's compose+plan phase below verifies that fix end to
	// end. This entry stays because the probe's per-type phase
	// inspects the *enricher* output, which legitimately still lacks
	// public_key.
	"aws_key_pair": "public_key unrecoverable from any API — fixed at the composer layer, see #665",

	// aws_cloudfront_function was a confirmed gap (GetResource omits
	// code/runtime) — FIXED by the cloudfrontFunctionEnricher composite
	// (#665), which overlays both from the CloudFront API. It is no
	// longer listed here; the probe now asserts it enriches cleanly.
}

// e2eThrowawayPublicKey is a throwaway ed25519 public key for the
// aws_key_pair probe. The matching private key was generated and
// discarded at authoring time — a public key is not a secret, and
// ImportKeyPair only validates that the material is a well-formed key.
const e2eThrowawayPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBYRn+c+IPlLaBIAbZvs13Vq3XdJvTtWNvMdoeR9s/Ea io-e2e665-throwaway"

// TestLive665_CloudControlRequiredFieldProbe is the live detector.
func TestLive665_CloudControlRequiredFieldProbe(t *testing.T) {
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
	if _, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		t.Skipf("STS GetCallerIdentity failed (no usable AWS creds), skipping: %v", err)
	}

	clients := EnrichClients{
		CloudControl: cloudcontrol.NewFromConfig(cfg),
		CloudFront:   cloudfront.NewFromConfig(cfg),
	}
	disc := NewAWSDiscoverer(cfg)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// enriched collects the enriched IRs across the per-type subtests
	// so the compose+terraform-plan subtest can verify the composed
	// HCL end to end. Subtests run sequentially, so the append is safe.
	var enriched []imported.ImportedResource

	// Each probe provisions a resource and returns the resource name —
	// the value the discoverer stamps on Identity.NameHint — so the
	// probe can pick its resource out of the discovered set.
	probes := []struct {
		tfType    string
		provision func(t *testing.T) (name string, ok bool)
	}{
		{
			tfType: "aws_cloudwatch_log_resource_policy",
			provision: func(t *testing.T) (string, bool) {
				c := cloudwatchlogs.NewFromConfig(cfg)
				name := "io-e2e665-logpol-" + suffix
				doc := `{"Version":"2012-10-17","Statement":[{"Sid":"e2e","Effect":"Allow","Principal":{"Service":"route53.amazonaws.com"},"Action":["logs:PutLogEvents","logs:CreateLogStream"],"Resource":"*"}]}`
				if _, err := c.PutResourcePolicy(ctx, &cloudwatchlogs.PutResourcePolicyInput{
					PolicyName:     aws.String(name),
					PolicyDocument: aws.String(doc),
				}); err != nil {
					t.Logf("provision skipped: PutResourcePolicy: %v", err)
					return "", false
				}
				t.Cleanup(func() {
					if _, err := c.DeleteResourcePolicy(context.Background(), &cloudwatchlogs.DeleteResourcePolicyInput{
						PolicyName: aws.String(name),
					}); err != nil {
						t.Logf("cleanup DeleteResourcePolicy(%s): %v", name, err)
					}
				})
				return name, true
			},
		},
		{
			tfType: "aws_key_pair",
			provision: func(t *testing.T) (string, bool) {
				c := ec2.NewFromConfig(cfg)
				name := "io-e2e665-key-" + suffix
				if _, err := c.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
					KeyName:           aws.String(name),
					PublicKeyMaterial: []byte(e2eThrowawayPublicKey),
				}); err != nil {
					t.Logf("provision skipped: ImportKeyPair: %v", err)
					return "", false
				}
				t.Cleanup(func() {
					if _, err := c.DeleteKeyPair(context.Background(), &ec2.DeleteKeyPairInput{
						KeyName: aws.String(name),
					}); err != nil {
						t.Logf("cleanup DeleteKeyPair(%s): %v", name, err)
					}
				})
				return name, true
			},
		},
		{
			tfType: "aws_cloudfront_function",
			provision: func(t *testing.T) (string, bool) {
				c := cloudfront.NewFromConfig(cfg)
				name := "io-e2e665-fn-" + suffix
				code := []byte("function handler(event) { return event.request; }")
				if _, err := c.CreateFunction(ctx, &cloudfront.CreateFunctionInput{
					Name:         aws.String(name),
					FunctionCode: code,
					FunctionConfig: &cftypes.FunctionConfig{
						Comment: aws.String("io-e2e665 throwaway - safe to delete"),
						Runtime: cftypes.FunctionRuntimeCloudfrontJs20,
					},
				}); err != nil {
					t.Logf("provision skipped: CreateFunction: %v", err)
					return "", false
				}
				t.Cleanup(func() {
					desc, derr := c.DescribeFunction(context.Background(), &cloudfront.DescribeFunctionInput{Name: aws.String(name)})
					if derr != nil {
						t.Logf("cleanup DescribeFunction(%s): %v", name, derr)
						return
					}
					if _, err := c.DeleteFunction(context.Background(), &cloudfront.DeleteFunctionInput{
						Name:    aws.String(name),
						IfMatch: desc.ETag,
					}); err != nil {
						t.Logf("cleanup DeleteFunction(%s): %v", name, err)
					}
				})
				return name, true
			},
		},
	}

	// rootT owns the resource teardown: each provision registers its
	// t.Cleanup on rootT (not the per-type subtest) so the resources
	// outlive the per-type subtests and are still present for the
	// terraform-plan subtest, which imports them. A t.Cleanup on a
	// subtest fires when that subtest ends — far too early here.
	rootT := t
	for _, p := range probes {
		t.Run(p.tfType, func(t *testing.T) {
			name, ok := p.provision(rootT)
			if !ok {
				t.Skip("provisioning did not complete (see log)")
			}

			// Run the production discover path so the Identity (import
			// id, region, native ids) is exactly what a real import
			// would carry — no hand-constructed strawman identifier.
			discovered, err := disc.DiscoverTypes(ctx, []string{p.tfType}, DiscoverArgs{Regions: []string{region}})
			if err != nil {
				t.Fatalf("DiscoverTypes(%s): %v", p.tfType, err)
			}
			idx := -1
			for i := range discovered {
				id := discovered[i].Identity
				if id.NameHint == name || id.ImportID == name || strings.Contains(id.ImportID, name) {
					idx = i
					break
				}
			}
			if idx < 0 {
				t.Skipf("provisioned %s %q not found by DiscoverTypes (propagation lag?)", p.tfType, name)
			}

			irs := discovered[idx : idx+1]
			if err := disc.EnrichAttributes(ctx, irs, clients, nil); err != nil {
				t.Fatalf("EnrichAttributes(%s): %v", p.tfType, err)
			}
			enriched = append(enriched, irs[0])
			missing := missingRequiredFields(t, p.tfType, irs[0].Attrs)
			reason, known := expectedGaps[p.tfType]
			switch {
			case len(missing) > 0 && known:
				t.Logf("KNOWN GAP (%s): CloudControl did not populate %v — tracked: %s",
					p.tfType, missing, reason)
			case len(missing) > 0 && !known:
				t.Fatalf("NEW BUG-CLASS INSTANCE: %s — the CloudControl enrich path did not "+
					"populate required field(s) %v. This needs a hand-rolled enricher "+
					"(the #661 fix shape). Add it, or record the gap in expectedGaps "+
					"with a tracking issue.", p.tfType, missing)
			case len(missing) == 0 && known:
				t.Fatalf("%s is in expectedGaps but the probe found all required fields "+
					"populated — the gap is fixed; remove %q from expectedGaps.", p.tfType, p.tfType)
			default:
				t.Logf("OK (%s): all required fields populated by the CloudControl path", p.tfType)
			}
		})
	}

	// End-to-end verification: compose the enriched IRs to HCL and run
	// `terraform plan` against the live account. This proves the fixes
	// hold where it matters — a plan ERROR is the regression. For
	// aws_key_pair specifically, this is the only phase that exercises
	// the composer-layer fix (placeholder public_key + ignore_changes),
	// since the per-type phase above inspects only the enricher output.
	t.Run("terraform plan accepts the composed imported.tf", func(t *testing.T) {
		if len(enriched) == 0 {
			t.Skip("no resources enriched (all provisioning skipped)")
		}
		tfBin, lookErr := exec.LookPath("terraform")
		if lookErr != nil {
			t.Skip("terraform binary not on PATH")
		}
		out, _ := composer.EmitImportedTF("aws", enriched, composer.EmitImportedOpts{})
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

// missingRequiredFields returns the REQUIRED schema fields of tfType
// that are absent or empty in the enriched Attrs JSON. Generic — keyed
// off the generated schema — so it covers every required field of the
// type. An entirely empty Attrs payload (the enricher soft-failed) is
// reported as every required field missing, which is what it is.
func missingRequiredFields(t *testing.T, tfType string, attrs json.RawMessage) []string {
	t.Helper()
	_, schema, ok := generated.Lookup(tfType)
	require.Truef(t, ok, "%s not registered in generated", tfType)

	required := func() []string {
		var r []string
		for name, fs := range schema {
			if fs.Required {
				r = append(r, name)
			}
		}
		sort.Strings(r)
		return r
	}

	if len(attrs) == 0 {
		return required()
	}
	var m map[string]json.RawMessage
	require.NoErrorf(t, json.Unmarshal(attrs, &m), "%s: Attrs is not a JSON object", tfType)
	var missing []string
	for _, name := range required() {
		v, present := m[name]
		if !present || isEmptyJSONValue(v) {
			missing = append(missing, name)
		}
	}
	return missing
}

// isEmptyJSONValue reports whether a raw JSON value is absent-equivalent
// — null, an empty string, or an empty object/array.
func isEmptyJSONValue(v json.RawMessage) bool {
	switch strings.TrimSpace(string(v)) {
	case "", "null", `""`, "{}", "[]":
		return true
	}
	return false
}
