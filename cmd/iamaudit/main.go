// Command iamaudit records which IAM actions the customer-account deploy roles
// used, from CloudTrail, and diffs them against the deploy policy they will be
// narrowed to: Phase 1 of the least-privilege deploy role
// (luthersystems/sandbox-infrastructure-template#147; the runbook is
// docs/design/least-privilege-phase1-runbook.md there).
//
// Usage:
//
//	iamaudit -source s3 -bucket <trail bucket> -s3-prefix AWSLogs/<account> \
//	    -region us-west-2 -region us-east-1 \
//	    -role-regex ':role/[^/]+-luther-terraform$' \
//	    -start 2026-11-17T00:00:00Z -end 2026-11-24T00:00:00Z \
//	    -policy insideout-write.json -boundary insideout-deploy-boundary.json \
//	    -json report.json
//
// Sources: s3 (the trail's bucket), lookup (CloudTrail event history, no trail
// needed, 90 days, management events only) and file (log files or
// `aws cloudtrail lookup-events` output on disk).
//
// Exit codes: 0 when the Phase 2 gate passes (every used action is allowed,
// or was refused on every call already); 3 when it fails (-fail-on-gap=false
// turns that off); 2 on bad flags; 1 on any other error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/luthersystems/insideout-terraform-presets/pkg/iamaudit"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
	exitGap   = 3
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, awsSources))
}

// multi is a flag that may be given more than once. Each value is kept
// whole: a role regex or a path may hold a comma ({1,8}, a file name).
type multi []string

func (m *multi) String() string { return strings.Join(*m, ",") }

func (m *multi) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// regionList is a multi that also takes a comma-separated list, and keeps
// each region once: a region read twice would count its calls twice.
type regionList []string

func (m *regionList) String() string { return strings.Join(*m, ",") }

func (m *regionList) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(*m, s) {
			*m = append(*m, s)
		}
	}
	return nil
}

type options struct {
	source    string
	regions   regionList
	bucket    string
	bucketReg string
	s3Prefix  string
	files     multi
	roleARNs  multi
	roleRegex multi
	start     string
	end       string
	since     time.Duration
	policies  multi
	boundary  string
	format    string
	jsonOut   string
	failOnGap bool
	workers   int
}

// sourceFactory builds the AWS-backed sources; tests replace it so no test
// makes an AWS call.
type sourceFactory func(ctx context.Context, o *options) (iamaudit.Source, error)

func run(ctx context.Context, args []string, stdout, stderr io.Writer, newAWS sourceFactory) int {
	o, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		_, _ = fmt.Fprintln(stderr, "iamaudit:", err)
		return exitUsage
	}
	if err := audit(ctx, o, stdout, newAWS); err != nil {
		if errors.Is(err, errGap) {
			return exitGap
		}
		_, _ = fmt.Fprintln(stderr, "iamaudit:", err)
		return exitError
	}
	return exitOK
}

func parseFlags(args []string, stderr io.Writer) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("iamaudit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.source, "source", "s3", "where to read CloudTrail events: s3, lookup or file")
	fs.Var(&o.regions, "region", "region to read (repeatable; s3 and lookup). Include us-east-1: IAM, STS, CloudFront and Route 53 log there")
	fs.StringVar(&o.bucket, "bucket", "", "trail bucket (s3)")
	fs.StringVar(&o.bucketReg, "bucket-region", "", "region of the trail bucket (s3; default the first -region)")
	fs.StringVar(&o.s3Prefix, "s3-prefix", "", "key path down to the account: AWSLogs/<account>, or AWSLogs/<org-id>/<account> for an organization trail (s3)")
	fs.Var(&o.files, "file", "log file, lookup-events output or directory (repeatable; file)")
	fs.Var(&o.roleARNs, "role-arn", "ARN of a role to audit (repeatable)")
	fs.Var(&o.roleRegex, "role-regex", "regular expression on role ARNs to audit (repeatable)")
	fs.StringVar(&o.start, "start", "", "window start, RFC 3339")
	fs.StringVar(&o.end, "end", "", "window end, RFC 3339 (default now)")
	fs.DurationVar(&o.since, "since", 0, "window length back from -end, instead of -start (e.g. 168h)")
	fs.Var(&o.policies, "policy", "IAM policy JSON the roles will hold (repeatable; the union is evaluated)")
	fs.StringVar(&o.boundary, "boundary", "", "permissions boundary JSON that caps the policy")
	fs.StringVar(&o.format, "format", "text", "stdout format: text or json")
	fs.StringVar(&o.jsonOut, "json", "", "also write the JSON report to this file")
	fs.BoolVar(&o.failOnGap, "fail-on-gap", true, "exit 3 when the Phase 2 gate fails")
	fs.IntVar(&o.workers, "workers", 16, "log files read at once (s3)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if len(o.roleARNs) == 0 && len(o.roleRegex) == 0 {
		return nil, errors.New("give at least one -role-arn or -role-regex")
	}
	if len(o.policies) == 0 {
		return nil, errors.New("give at least one -policy")
	}
	if o.format != "text" && o.format != "json" {
		return nil, fmt.Errorf("-format %q: want text or json", o.format)
	}
	switch o.source {
	case "s3":
		if o.bucket == "" || o.s3Prefix == "" || len(o.regions) == 0 {
			return nil, errors.New("-source s3 needs -bucket, -s3-prefix and -region")
		}
	case "lookup":
		if len(o.regions) == 0 {
			return nil, errors.New("-source lookup needs -region")
		}
	case "file":
		if len(o.files) == 0 {
			return nil, errors.New("-source file needs -file")
		}
	default:
		return nil, fmt.Errorf("-source %q: want s3, lookup or file", o.source)
	}
	if o.since < 0 {
		return nil, fmt.Errorf("-since %s: want a positive length", o.since)
	}
	if o.start != "" && o.since != 0 {
		return nil, errors.New("give -start or -since, not both")
	}
	if o.source != "file" && o.start == "" && o.since == 0 {
		return nil, errors.New("give -start or -since: an AWS source must bound what it reads")
	}
	return o, nil
}

var errGap = errors.New("used actions not allowed")

func audit(ctx context.Context, o *options, stdout io.Writer, newAWS sourceFactory) error {
	start, end, err := window(o, time.Now())
	if err != nil {
		return err
	}
	pats, err := iamaudit.CompileRolePatterns(o.roleRegex)
	if err != nil {
		return err
	}
	policies := make([]*iamaudit.Policy, 0, len(o.policies))
	for _, p := range o.policies {
		pol, err := readPolicy(p)
		if err != nil {
			return err
		}
		policies = append(policies, pol)
	}
	var boundary *iamaudit.Policy
	if o.boundary != "" {
		if boundary, err = readPolicy(o.boundary); err != nil {
			return err
		}
	}

	var src iamaudit.Source
	if o.source == "file" {
		src = &iamaudit.FileSource{Paths: o.files}
	} else if src, err = newAWS(ctx, o); err != nil {
		return err
	}
	c := &iamaudit.Collector{Roles: iamaudit.RoleFilter{ARNs: o.roleARNs, Patterns: pats}, Start: start, End: end}
	if err := src.Feed(ctx, c); err != nil {
		return err
	}

	rep := iamaudit.Diff(c.Result(), iamaudit.DiffOptions{
		Source:   o.source,
		Start:    start,
		End:      end,
		Policies: policies,
		Boundary: boundary,
		Notes:    notes(o),
	})
	if o.jsonOut != "" {
		if err := writeJSON(o.jsonOut, &rep); err != nil {
			return err
		}
	}
	if o.format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(&rep); err != nil {
			return err
		}
	} else if err := rep.WriteText(stdout); err != nil {
		return err
	}
	if o.failOnGap && !rep.OK() {
		return errGap
	}
	return nil
}

// window resolves -start, -end and -since into [start, end).
func window(o *options, now time.Time) (time.Time, time.Time, error) {
	end := now.UTC()
	if o.end != "" {
		t, err := time.Parse(time.RFC3339, o.end)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("-end: %w", err)
		}
		end = t.UTC()
	}
	var start time.Time
	switch {
	case o.start != "":
		t, err := time.Parse(time.RFC3339, o.start)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("-start: %w", err)
		}
		start = t.UTC()
	case o.since > 0:
		start = end.Add(-o.since)
	}
	if !start.IsZero() && !start.Before(end) {
		return time.Time{}, time.Time{}, errors.New("the window is empty: -start is not before -end")
	}
	return start, end, nil
}

// notes are the caveats the report carries about what the source could not
// see, so a reader of the JSON alone does not over-trust an empty diff.
func notes(o *options) []string {
	out := []string{
		"iam:PassRole and session tagging are authorized without an event of their own; they are listed as unobservable, not unused.",
	}
	switch o.source {
	case "lookup":
		out = append(out, "Event history holds management events only: S3 object, Lambda invoke and other data events are not in this report.")
	case "s3":
		out = append(out, "Data events are in this report only if the trail logs them.")
	}
	hasEast := false
	for _, r := range o.regions {
		hasEast = hasEast || r == "us-east-1"
	}
	if o.source != "file" && !hasEast {
		out = append(out, "us-east-1 was not read: IAM, global STS, CloudFront and Route 53 calls are logged there and are missing.")
	}
	return out
}

func readPolicy(path string) (*iamaudit.Policy, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator names the policy file
	if err != nil {
		return nil, err
	}
	return iamaudit.ParsePolicy(path, data)
}

func writeJSON(path string, rep *iamaudit.Report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600) // #nosec G703 -- the operator names the -json output file
}

// awsSources builds the s3 or lookup source with the default credential
// chain (AWS_PROFILE, the environment, SSO, …).
func awsSources(ctx context.Context, o *options) (iamaudit.Source, error) {
	switch o.source {
	case "s3":
		// The SDK does not follow S3's redirect to a bucket in another
		// region, so the client must be in the bucket's region.
		region := o.bucketReg
		if region == "" {
			region = o.regions[0]
		}
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return nil, err
		}
		return &iamaudit.S3Source{
			Client:  s3.NewFromConfig(cfg),
			Bucket:  o.bucket,
			Prefix:  o.s3Prefix,
			Regions: o.regions,
			Workers: o.workers,
		}, nil
	case "lookup":
		clients := map[string]iamaudit.LookupEventsAPI{}
		for _, r := range o.regions {
			cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(r))
			if err != nil {
				return nil, err
			}
			clients[r] = cloudtrail.NewFromConfig(cfg)
		}
		return &iamaudit.LookupSource{Clients: clients, Interval: 500 * time.Millisecond}, nil
	}
	return nil, fmt.Errorf("-source %q is not an AWS source", o.source)
}
