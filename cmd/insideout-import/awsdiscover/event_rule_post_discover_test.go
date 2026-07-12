package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeEventRuleDescriber is a minimal eventRuleDescriber stub for the
// PostDiscover hook tests. It records the DescribeRule input so the tests can
// assert the derived Name / EventBusName.
type fakeEventRuleDescriber struct {
	out            *eventbridge.DescribeRuleOutput
	err            error
	gotName        string
	gotEventBusPtr *string
}

func (f *fakeEventRuleDescriber) DescribeRule(_ context.Context, in *eventbridge.DescribeRuleInput, _ ...func(*eventbridge.Options)) (*eventbridge.DescribeRuleOutput, error) {
	f.gotName = aws.ToString(in.Name)
	f.gotEventBusPtr = in.EventBusName
	return f.out, f.err
}

// TestEventRulePostDiscover_ManagedRuleClassified is the ccs-101d5181-5czmw
// regression: AutoScalingManagedRule on the default bus has its ManagedBy
// resolved at DISCOVER time via events:DescribeRule and stamped onto
// Identity.ServiceManagedBy, so the shared importability classifier excludes
// it into unsupported.json instead of letting the imported Project-tag stamp
// fail the whole apply with ManagedRuleException. CC omits ManagedBy from its
// schema, which is why the props fast-path alone was insufficient.
func TestEventRulePostDiscover_ManagedRuleClassified(t *testing.T) {
	t.Parallel()
	fake := &fakeEventRuleDescriber{
		out: &eventbridge.DescribeRuleOutput{
			Name:      aws.String("AutoScalingManagedRule"),
			ManagedBy: aws.String("autoscaling.amazonaws.com"),
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_event_rule",
			Region:   "us-east-1",
			NameHint: "AutoScalingManagedRule",
			ImportID: "default/AutoScalingManagedRule",
			NativeIDs: map[string]string{
				"arn": "arn:aws:events:us-east-1:111111111111:rule/AutoScalingManagedRule",
			},
		},
	}
	require.NoError(t, eventRulePostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "autoscaling.amazonaws.com", ir.Identity.ServiceManagedBy,
		"ManagedBy must be backfilled for the classifier")
	// Bare rule name is used for DescribeRule; the default bus is omitted.
	assert.Equal(t, "AutoScalingManagedRule", fake.gotName)
	assert.Nil(t, fake.gotEventBusPtr, "default bus must not pass EventBusName")
	// The shared classifier now routes it to unsupported.json.
	assert.Equal(t, imported.ReasonServiceManaged, imported.UnimportableReason(*ir),
		"managed rule must classify as un-importable once ManagedBy is stamped")
}

// TestEventRulePostDiscover_CustomBusPassesBusName proves a rule on a custom
// event bus passes EventBusName to DescribeRule (the default bus is omitted).
func TestEventRulePostDiscover_CustomBusPassesBusName(t *testing.T) {
	t.Parallel()
	fake := &fakeEventRuleDescriber{
		out: &eventbridge.DescribeRuleOutput{
			Name:      aws.String("my-rule"),
			ManagedBy: aws.String("some.service.amazonaws.com"),
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_event_rule",
			NameHint: "my-rule",
			ImportID: "my-bus/my-rule",
		},
	}
	require.NoError(t, eventRulePostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "my-rule", fake.gotName)
	require.NotNil(t, fake.gotEventBusPtr, "custom bus must pass EventBusName")
	assert.Equal(t, "my-bus", aws.ToString(fake.gotEventBusPtr))
	assert.Equal(t, "some.service.amazonaws.com", ir.Identity.ServiceManagedBy)
}

// TestEventRulePostDiscover_CustomerRuleImportable proves a customer rule (no
// ManagedBy) stays importable: ServiceManagedBy is left empty and
// UnimportableReason returns "".
func TestEventRulePostDiscover_CustomerRuleImportable(t *testing.T) {
	t.Parallel()
	fake := &fakeEventRuleDescriber{
		out: &eventbridge.DescribeRuleOutput{Name: aws.String("my-app-rule")},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_event_rule",
			NameHint: "my-app-rule",
			ImportID: "default/my-app-rule",
		},
	}
	require.NoError(t, eventRulePostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "", ir.Identity.ServiceManagedBy, "customer rule carries no ManagedBy")
	assert.Equal(t, "", imported.UnimportableReason(*ir), "customer rule must remain importable")
}

// TestEventRulePostDiscover_SoftFailsOnError proves a DescribeRule failure is
// surfaced as an error (the discoverer logs it via ServiceWarn) without
// clobbering the IR — the rule is still emitted as importable, matching the
// genconfig prune backstop posture.
func TestEventRulePostDiscover_SoftFailsOnError(t *testing.T) {
	t.Parallel()
	fake := &fakeEventRuleDescriber{err: errors.New("AccessDenied")}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_event_rule",
			NameHint: "my-app-rule",
			ImportID: "default/my-app-rule",
		},
	}
	require.Error(t, eventRulePostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "", ir.Identity.ServiceManagedBy, "no marker stamped when DescribeRule fails")
	assert.Equal(t, "", imported.UnimportableReason(*ir), "un-resolvable rule stays importable (backstop posture)")
}

// TestEventRulePostDiscover_EmptyIdentity proves a rule whose name cannot be
// derived returns an error rather than panicking or issuing a garbage call.
func TestEventRulePostDiscover_EmptyIdentity(t *testing.T) {
	t.Parallel()
	fake := &fakeEventRuleDescriber{}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_event_rule"}}
	require.Error(t, eventRulePostDiscoverWithClient(context.Background(), fake, ir))
}

// TestEventRuleConfig_WiresPostDiscover guards the registration: the
// aws_cloudwatch_event_rule cloudControlConfig must carry the PostDiscover
// hook, or the discover-time ManagedBy backfill silently regresses (CC omits
// ManagedBy, so the props fast-path alone never fires — the ccs-101d5181-5czmw
// class of failure).
func TestEventRuleConfig_WiresPostDiscover(t *testing.T) {
	t.Parallel()
	var found bool
	for _, cfg := range cloudControlTypeConfigs {
		if cfg.TFType == "aws_cloudwatch_event_rule" {
			found = true
			require.NotNil(t, cfg.PostDiscover, "aws_cloudwatch_event_rule must wire PostDiscover for discover-time ManagedBy backfill")
		}
	}
	require.True(t, found, "aws_cloudwatch_event_rule config not found")
}
