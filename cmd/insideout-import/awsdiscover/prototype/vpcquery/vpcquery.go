// Package vpcquery is a research prototype (issue #339) that demonstrates
// using Terraform 1.14's `terraform query -json` command as a replacement
// for the List+filter half of an AWS discoverer. Target type: aws_vpc.
//
// This package is OPT-IN. The production aws_vpc discoverer remains in
// cmd/insideout-import/awsdiscover/vpc.go and is registered in
// awsdiscover.NewAWSDiscovererWithConcurrency. Nothing in the production
// pipeline imports this package; it exists for benchmarking, decision-
// making, and as a reference implementation for the follow-up "should we
// migrate the other 27 covered AWS types?" ticket.
//
// # Shape
//
//  1. vpc.tfquery.hcl + providers.tf.tmpl are embedded via embed.FS.
//  2. Discoverer.Discover renders both into an os.MkdirTemp workdir per
//     region, runs `terraform init -input=false` once and
//     `terraform query -json -var project_filter=<…>` per region.
//  3. The streaming JSON line-protocol (one JSON object per line on stdout)
//     is parsed for `type=list_resource_found` events; each carries an
//     `identity.id` (vpc-XXXXXXXX) and `display_name`.
//  4. For each match, the wrapper does a follow-up DescribeVpcs(VpcIds=[id])
//     via the existing vpcClient surface to fetch tags and CIDR. That
//     second round-trip is what keeps Identity.Tags populated; the AWS
//     provider's list_resource_schema for aws_vpc exposes ONLY region,
//     vpc_ids, and a filter block — no tag attribute, no tag passthrough
//     on matched records. (verified: terraform providers schema -json
//     against hashicorp/aws v6.44.0).
//  5. Identity construction reuses the parent package's makeImportedResource
//     via an exported shim — no IR shape divergence between the two paths.
//
// # What this prototype proves
//
//   - The tfquery.hcl + JSON pipe works end-to-end against a real AWS
//     account (CUST3 / 031780745048 / us-east-1 — see live-smoke note in
//     docs/terraform-query-prototype.md).
//   - The provider exposes the same server-side filter primitives as the
//     EC2 SDK, so Project-tag filtering migrates 1:1.
//   - Tag fetch is NOT subsumed by `terraform query` — Identity.Tags
//     still requires a second AWS API call. This kills the "75% LOC
//     savings" hypothesis from the issue (real savings ~30-40%, see decision
//     doc).
//   - Per-region scoping requires re-rendering providers.tf (or sticking
//     a per-region provider alias in one file). Either way, scratch-dir
//     management is on the wrapper.
//
// # What this prototype does NOT do
//
//   - DiscoverByID — the issue scope is Stage 2a (bulk discover) only.
//     Single-resource lookup stays on the SDK because `terraform query`
//     would be a 5x latency hit (init + query) for a single ID.
//   - Replace the parent vpcDiscoverer in production. Wiring this into
//     the aggregator is a follow-up only if the decision is "migrate."
package vpcquery

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

//go:embed vpc.tfquery.hcl
var vpcQueryHCL []byte

//go:embed providers.tf.tmpl
var providersTmplSrc string

// providersTmpl is parsed once at package init; rendering per region is a
// cheap text/template Execute. Failing here at init means the embed and
// the template are catastrophically out of sync, which is a build-time
// bug rather than a runtime one.
var providersTmpl = template.Must(template.New("providers").Parse(providersTmplSrc))

// VpcClient is the narrow EC2 surface this prototype consumes. It mirrors
// the parent package's vpcClient interface (which is unexported) so unit
// tests can plug in a fake without dragging in the real EC2 SDK. The
// production wrapper builds a real ec2.Client from the supplied aws.Config.
type VpcClient interface {
	DescribeVpcs(ctx context.Context, in *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
}

// QueryRunner abstracts `terraform init` + `terraform query -json` so unit
// tests can avoid shelling out. Production wires up TerraformBinaryRunner
// (below); tests inject a recorded-event runner.
//
// Run returns the raw bytes of the terraform-query stdout (newline-
// delimited JSON, one event per line). It is the caller's responsibility
// to parse the lines — keeping the runner dumb makes the contract small
// enough to mock cleanly.
type QueryRunner interface {
	Run(ctx context.Context, workDir, region, projectFilter string) ([]byte, error)
}

// Discoverer is the Stage 2a entry point for the tfquery-backed aws_vpc
// path. Constructed via NewDiscoverer; the test surface wires the fields
// directly.
//
// This type intentionally does NOT implement awsdiscover.Discoverer —
// while the Discover signature matches, registering it in
// NewAWSDiscovererWithConcurrency would require also providing a
// DiscoverByID, and the prototype scope (#339) is "bulk discover only."
// A follow-up migration ticket would either (a) add DiscoverByID here
// or (b) compose this with the SDK-only DiscoverByID from vpc.go.
type Discoverer struct {
	// runner shells out to `terraform query`. Must be non-nil.
	runner QueryRunner
	// new builds a per-region EC2 client used for the tag-fetch round-trip
	// (terraform query does not return tags — see package doc).
	new func(region string) VpcClient
}

// NewDiscoverer builds a production-ready prototype Discoverer. The
// runner shells out to the operator's terraform binary on $PATH (which
// MUST be Terraform 1.14+ — earlier versions reject the `list { }`
// block in vpc.tfquery.hcl with a parse error).
func NewDiscoverer(cfg aws.Config) *Discoverer {
	return &Discoverer{
		runner: TerraformBinaryRunner{},
		new: func(region string) VpcClient {
			return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
	}
}

// ResourceType returns the Terraform type this prototype covers. Mirrors
// the parent vpcDiscoverer.ResourceType so the two are interchangeable
// from the aggregator's perspective.
func (d *Discoverer) ResourceType() string { return "aws_vpc" }

// Discover runs `terraform query` once per region, parses the resulting
// list_resource_found events, fetches tags for each match, and returns
// []imported.ImportedResource. Output is sorted by VPC ID for parity
// with the parent vpcDiscoverer.
//
// Project-tag filter: passed to the query as -var project_filter=<…>.
// Empty filter disables the server-side EC2 filter — the admin/audit
// path. The TagSelectors AND-conjunction is enforced client-side after
// the tag fetch (parity with parent).
//
// Multi-region (#291): one query invocation per region, mirroring the
// parent's `for _, region := range args.Regions` loop. Each region gets
// its own scratch workdir (so terraform init's per-provider lockfile
// and .terraform plugin cache don't collide if we ever parallelize).
func (d *Discoverer) Discover(ctx context.Context, args awsdiscover.DiscoverArgs) ([]imported.ImportedResource, error) {
	if d.runner == nil {
		return nil, errors.New("vpcquery: nil QueryRunner")
	}
	if d.new == nil {
		return nil, errors.New("vpcquery: nil VpcClient factory")
	}

	var out []imported.ImportedResource
	for _, region := range args.Regions {
		regionStart := time.Now()
		hits, err := d.discoverRegion(ctx, region, args)
		if err != nil {
			return nil, fmt.Errorf("vpcquery (region=%s): %w", region, err)
		}
		out = append(out, hits...)
		_ = regionStart // emitter integration left to follow-up if migration goes ahead
	}
	return out, nil
}

// discoverRegion handles one region: render the workdir, run terraform
// init+query, parse events, fetch tags, build ImportedResources.
func (d *Discoverer) discoverRegion(ctx context.Context, region string, args awsdiscover.DiscoverArgs) ([]imported.ImportedResource, error) {
	workDir, cleanup, err := d.prepareWorkDir(region)
	if err != nil {
		return nil, fmt.Errorf("prepare workdir: %w", err)
	}
	defer cleanup()

	stdout, err := d.runner.Run(ctx, workDir, region, args.Project)
	if err != nil {
		return nil, fmt.Errorf("terraform query: %w", err)
	}

	matches, err := parseQueryEvents(stdout)
	if err != nil {
		return nil, fmt.Errorf("parse events: %w", err)
	}

	// Sort by VPC ID for deterministic ImportedResource address generation.
	// Parity with vpc.go::Discover which sorts vpcs by ID before the loop.
	sort.Slice(matches, func(i, j int) bool { return matches[i].VPCID < matches[j].VPCID })

	if len(matches) == 0 {
		return nil, nil
	}

	client := d.new(region)
	ids := make([]string, 0, len(matches))
	for _, m := range matches {
		ids = append(ids, m.VPCID)
	}

	// Single DescribeVpcs(VpcIds=[…]) round-trip: avoids one-call-per-
	// VPC when the match set is small enough to fit in a single page
	// (1000 IDs, EC2's hard cap). For accounts with > 1000 matches the
	// follow-up migration would chunk this — the prototype keeps the
	// simple path because the production hand-written discoverer
	// doesn't need to either (the inline DescribeVpcs already returns
	// tags for everything).
	resp, err := client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: ids})
	if err != nil {
		return nil, fmt.Errorf("DescribeVpcs (tags): %w", err)
	}

	byID := make(map[string]ec2types.Vpc, len(resp.Vpcs))
	for i := range resp.Vpcs {
		byID[aws.ToString(resp.Vpcs[i].VpcId)] = resp.Vpcs[i]
	}

	book := awsdiscover.NewAddressBook()
	out := make([]imported.ImportedResource, 0, len(matches))
	for _, m := range matches {
		v, ok := byID[m.VPCID]
		if !ok {
			// Race between query and tag fetch (rare, but possible):
			// VPC was deleted between the two calls. Skip and let the
			// next discover run pick up the missing entry.
			continue
		}
		tags := ec2TagsToMap(v.Tags)
		if !awsdiscover.MatchesAll(tags, args.TagSelectors) {
			continue
		}
		name := vpcName(v.Tags, m.VPCID)
		out = append(out, awsdiscover.MakeImportedResource(
			book,
			"aws_vpc",
			name,
			m.VPCID,
			region,
			args.AccountID,
			map[string]string{
				"vpc_id":     m.VPCID,
				"cidr_block": aws.ToString(v.CidrBlock),
			},
			tags,
		))
	}
	return out, nil
}

// prepareWorkDir creates a scratch directory under os.TempDir and writes
// vpc.tfquery.hcl + a per-region providers.tf into it. Returns the path
// plus a cleanup callback that os.RemoveAll's the dir.
//
// Each region gets its own dir so concurrent regional queries don't
// collide on .terraform plugin state, and so that a half-failed
// terraform init (network blip) in one region doesn't poison the cache
// for the next.
func (d *Discoverer) prepareWorkDir(region string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "insideout-vpcquery-"+region+"-*")
	if err != nil {
		return "", nil, err
	}

	if err := os.WriteFile(filepath.Join(dir, "vpc.tfquery.hcl"), vpcQueryHCL, 0o600); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}

	var buf bytes.Buffer
	if err := providersTmpl.Execute(&buf, struct{ Region string }{Region: region}); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("render providers.tf: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "providers.tf"), buf.Bytes(), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}

	cleanup := func() { os.RemoveAll(dir) }
	return dir, cleanup, nil
}

// queryMatch is one entry parsed from the streaming JSON event log.
// VPCID is the AWS VPC ID (vpc-XXXXXXXX); DisplayName is the provider's
// human-friendly label (e.g. "io-foo-prod-vpc-vpc0 (vpc-...)") which we
// currently ignore — the Name tag from DescribeVpcs is preferred.
type queryMatch struct {
	VPCID       string
	DisplayName string
}

// listResourceFoundEvent is the subset of the type=list_resource_found
// JSON envelope this prototype consumes. Schema observed against
// terraform 1.14.9 + hashicorp/aws 6.44.0:
//
//	{
//	  "type": "list_resource_found",
//	  "list_resource_found": {
//	    "address": "list.aws_vpc.all",
//	    "display_name": "io-foo-prod-vpc (vpc-052c…)",
//	    "identity": {"account_id": "...", "id": "vpc-…", "region": "..."},
//	    "identity_version": 0,
//	    "resource_type": "aws_vpc"
//	  },
//	  ...
//	}
//
// Other event types in the stream we ignore: version, list_start,
// list_complete. The "type" field is the discriminator.
type listResourceFoundEvent struct {
	Type             string `json:"type"`
	ListResourceData struct {
		Address     string `json:"address"`
		DisplayName string `json:"display_name"`
		Identity    struct {
			AccountID string `json:"account_id"`
			ID        string `json:"id"`
			Region    string `json:"region"`
		} `json:"identity"`
		ResourceType string `json:"resource_type"`
	} `json:"list_resource_found"`
}

// parseQueryEvents walks the newline-delimited JSON event stream produced
// by `terraform query -json` and extracts every list_resource_found
// event whose resource_type is aws_vpc. Returns ([]queryMatch, nil) on
// success.
//
// Lines that don't decode as JSON (defensive: future Terraform versions
// might add framing) are skipped silently. Lines whose type is not
// list_resource_found are also skipped.
func parseQueryEvents(stdout []byte) ([]queryMatch, error) {
	var out []queryMatch
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	// Default scanner buffer (64 KiB) is too small for accounts with
	// many tags or long display_names — bump to 1 MiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev listResourceFoundEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Tolerate alien event types (e.g. diagnostics with @-prefixed
			// keys). A malformed line shouldn't kill the whole parse —
			// the per-line type filter below catches everything we care
			// about.
			continue
		}
		if ev.Type != "list_resource_found" {
			continue
		}
		if ev.ListResourceData.ResourceType != "aws_vpc" {
			continue
		}
		if ev.ListResourceData.Identity.ID == "" {
			continue
		}
		out = append(out, queryMatch{
			VPCID:       ev.ListResourceData.Identity.ID,
			DisplayName: ev.ListResourceData.DisplayName,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return out, nil
}

// ec2TagsToMap converts the EC2 SDK []Tag slice into a string-keyed map.
// Returns a non-nil empty map when the input is empty so the
// nil-vs-empty contract holds (#255 / parent vpc.go).
func ec2TagsToMap(in []ec2types.Tag) map[string]string {
	out := make(map[string]string, len(in))
	for _, t := range in {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}

// vpcName picks the most useful human-readable name for a VPC: the Name
// tag if set, otherwise the bare VPC ID. Matches the parent
// vpc.go::vpcName so prototype output addresses parallel production.
func vpcName(tags []ec2types.Tag, fallback string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == "Name" {
			if v := aws.ToString(t.Value); v != "" {
				return v
			}
		}
	}
	return fallback
}

// TerraformBinaryRunner is the production QueryRunner. It looks up
// `terraform` on $PATH, runs `terraform init -input=false -no-color`
// once in the workdir, then `terraform query -json -no-color
// -var project_filter=<…>` and returns the captured stdout.
//
// stderr is captured and propagated only on non-zero exit — successful
// runs throw it away. Both calls inherit the parent process environment
// so AWS_PROFILE / AWS_REGION / AWS_ACCESS_KEY_ID etc. flow through to
// the AWS provider as the operator expects.
type TerraformBinaryRunner struct{}

// Run shells out to `terraform`. Documented constraint: the terraform
// binary on PATH must be 1.14+. Earlier versions reject the list { }
// block in vpc.tfquery.hcl at parse time.
func (TerraformBinaryRunner) Run(ctx context.Context, workDir, region, projectFilter string) ([]byte, error) {
	bin, err := exec.LookPath("terraform")
	if err != nil {
		return nil, fmt.Errorf("terraform not on PATH (require 1.14+): %w", err)
	}

	// terraform init: idempotent, fast on warm cache. We re-run it per
	// invocation because the workdir is freshly created.
	initCmd := exec.CommandContext(ctx, bin, "init", "-input=false", "-no-color")
	initCmd.Dir = workDir
	var initErr bytes.Buffer
	initCmd.Stderr = &initErr
	if err := initCmd.Run(); err != nil {
		return nil, fmt.Errorf("terraform init: %w (stderr: %s)", err, strings.TrimSpace(initErr.String()))
	}

	args := []string{"query", "-json", "-no-color", "-var", "project_filter=" + projectFilter}
	queryCmd := exec.CommandContext(ctx, bin, args...)
	queryCmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	queryCmd.Stdout = &stdout
	queryCmd.Stderr = &stderr
	if err := queryCmd.Run(); err != nil {
		// Keep stderr in the error so operators see provider auth /
		// network errors without re-running with verbose flags.
		return nil, fmt.Errorf("terraform query: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Sanity check: the embed.FS-backed bytes are non-empty at init time.
// A missing file would break `go build`, but a zero-byte file would
// silently pass and produce a confusing runtime error from `terraform
// init` instead. Crashing here makes the failure obvious.
func init() {
	if len(vpcQueryHCL) == 0 {
		panic("vpcquery: vpc.tfquery.hcl is empty — embed directive broken")
	}
	if providersTmplSrc == "" {
		panic("vpcquery: providers.tf.tmpl is empty — embed directive broken")
	}
}

// Compile-time guard: parsing helpers shouldn't return non-nil VPCID for
// missing identity. The runtime check inside parseQueryEvents enforces
// this; this assignment exists only to keep the io and exec imports
// from being elided when the test build trims the binary runner via
// build tags (future).
var _ io.Reader = bytes.NewReader(nil)
