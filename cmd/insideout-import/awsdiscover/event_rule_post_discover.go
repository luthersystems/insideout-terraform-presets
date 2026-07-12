package awsdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// EventBridge rule PostDiscover follow-up — service-managed marker backfill.
//
// AWS-managed EventBridge rules (e.g. AutoScalingManagedRule, managed by
// autoscaling.amazonaws.com) cannot be adopted into customer Terraform
// state: AWS rejects tag / PutRule / DeleteRule on any rule with ManagedBy
// set with a ManagedRuleException. Stamping the imported Project tag onto
// such a rule in imported.tf fails the WHOLE apply — proven on staging apply
// ccs-101d5181-5czmw, where AutoScalingManagedRule (arn
// …:rule/AutoScalingManagedRule) sank a 264-import run with
// "ManagedRuleException: Tag related operations on the managed rule … not
// allowed".
//
// imported.UnimportableReason already classifies a rule as un-importable
// when Identity.ServiceManagedBy is non-empty (ReasonServiceManaged, #785).
// The gap: the Cloud Control AWS::Events::Rule schema does NOT expose
// ManagedBy among its read-only properties, so the discoverer's
// ServiceManagedByFromProperties extractor always returns "" and the
// classifier never fires. The hand-rolled props fast-path is kept only for
// the (rare) case CC does surface ManagedBy — the authoritative marker must
// be resolved at DISCOVER time.
//
// eventRulePostDiscover issues one events:DescribeRule per discovered rule
// and stamps Identity.ServiceManagedBy from the API's ManagedBy field so the
// shared classifier excludes AWS-managed rules into unsupported.json rather
// than letting them fall through and fail the apply. Soft-fails (returns an
// error the discoverer logs) when the rule name is unresolvable or
// DescribeRule fails — a customer rule with no ManagedBy is still treated as
// importable, the same posture as the genconfig prune backstop (#708) and
// the kms_key / network_acl PostDiscover hooks.

// defaultEventBusName is EventBridge's implicit default event bus. A rule on
// the default bus can be described without an explicit EventBusName, so the
// hook only passes EventBusName for a custom bus.
const defaultEventBusName = "default"

// eventRuleDescriber is the narrow subset of the EventBridge API the
// PostDiscover hook issues. Real *eventbridge.Client and in-test fakes
// satisfy it; the production hook constructs the real client per region.
type eventRuleDescriber interface {
	DescribeRule(ctx context.Context, in *eventbridge.DescribeRuleInput, opts ...func(*eventbridge.Options)) (*eventbridge.DescribeRuleOutput, error)
}

// newEventRuleDescriber is the production factory; tests swap it (or call
// eventRulePostDiscoverWithClient directly) to inject a fake.
var newEventRuleDescriber = func(awsCfg aws.Config, region string) eventRuleDescriber {
	return eventbridge.NewFromConfig(awsCfg, func(o *eventbridge.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// eventRulePostDiscover is the cloudControlConfig.PostDiscover hook for
// aws_cloudwatch_event_rule. It resolves ManagedBy via events:DescribeRule
// and stamps it onto Identity.ServiceManagedBy so imported.UnimportableReason
// can classify AWS-managed rules. Soft-fails on an SDK error (the resource is
// still emitted; a follow-up SDK miss must not drop an otherwise-importable
// rule).
func eventRulePostDiscover(ctx context.Context, awsCfg aws.Config, region string, ir *imported.ImportedResource) error {
	return eventRulePostDiscoverWithClient(ctx, newEventRuleDescriber(awsCfg, region), ir)
}

func eventRulePostDiscoverWithClient(ctx context.Context, client eventRuleDescriber, ir *imported.ImportedResource) error {
	if ir == nil {
		return nil
	}
	name, busName := eventRuleNameForDescribe(&ir.Identity)
	if name == "" {
		return fmt.Errorf("event_rule: cannot derive rule name from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	in := &eventbridge.DescribeRuleInput{Name: aws.String(name)}
	if busName != "" && busName != defaultEventBusName {
		in.EventBusName = aws.String(busName)
	}
	out, err := client.DescribeRule(ctx, in)
	if err != nil {
		return fmt.Errorf("event_rule %q: DescribeRule: %w", name, err)
	}
	if out == nil {
		return fmt.Errorf("event_rule %q: DescribeRule returned no rule", name)
	}
	if mb := aws.ToString(out.ManagedBy); mb != "" {
		ir.Identity.ServiceManagedBy = mb
	}
	return nil
}

// eventRuleNameForDescribe resolves the bare rule name and event-bus name the
// DescribeRule call needs from the identity the aws_cloudwatch_event_rule
// discoverer populates. NameHint carries the bare rule Name (the CFN `Name`
// property); ImportID is the `<bus>/<name>` form, so the bus name — and a
// fallback rule name — parse out of it.
func eventRuleNameForDescribe(id *imported.ResourceIdentity) (name, busName string) {
	if id == nil {
		return "", ""
	}
	if imp := strings.TrimSpace(id.ImportID); imp != "" {
		if idx := strings.LastIndex(imp, "/"); idx != -1 {
			busName = imp[:idx]
			name = imp[idx+1:]
		} else {
			name = imp
		}
	}
	if nh := strings.TrimSpace(id.NameHint); nh != "" {
		name = nh
	}
	return name, busName
}
