package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// fakeASGTagSmithyErr — smithy.APIError with a configurable code so we
// can exercise the NotFound mapping branches.
type fakeASGTagSmithyErr struct{ code string }

func (e *fakeASGTagSmithyErr) Error() string                 { return e.code }
func (e *fakeASGTagSmithyErr) ErrorCode() string             { return e.code }
func (e *fakeASGTagSmithyErr) ErrorMessage() string          { return e.code }
func (e *fakeASGTagSmithyErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

var _ smithy.APIError = (*fakeASGTagSmithyErr)(nil)

func TestAutoscalingGroupTagEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_autoscaling_group_tag",
		newAutoscalingGroupTagEnricher().ResourceType())
}

func TestAutoscalingGroupTagEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"autoscaling_group_name": "asg", "key": "k"},
		},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestAutoscalingGroupTagEnricher_CannotResolve(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{},
	}, EnrichClients{AutoScaling: &autoscaling.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve")
}

func TestAutoscalingGroupTagEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{fetch: func(_ context.Context, _ *autoscaling.Client, asg, key string) (string, bool, bool, error) {
		assert.Equal(t, "my-asg", asg)
		assert.Equal(t, "Environment", key)
		return "prod", true, true, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		NativeIDs: map[string]string{
			"autoscaling_group_name": "my-asg",
			"key":                    "Environment",
		},
		ImportID: "my-asg,Environment",
	}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{AutoScaling: &autoscaling.Client{}}))

	var got generated.AWSAutoscalingGroupTag
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.AutoscalingGroupName)
	assert.Equal(t, "my-asg", *got.AutoscalingGroupName.Literal)
	require.NotNil(t, got.ID)
	assert.Equal(t, "my-asg,Environment", *got.ID.Literal)
	require.Len(t, got.Tag, 1)
	require.NotNil(t, got.Tag[0].Key)
	assert.Equal(t, "Environment", *got.Tag[0].Key.Literal)
	require.NotNil(t, got.Tag[0].Value)
	assert.Equal(t, "prod", *got.Tag[0].Value.Literal)
	require.NotNil(t, got.Tag[0].PropagateAtLaunch)
	assert.Equal(t, true, *got.Tag[0].PropagateAtLaunch.Literal)
}

func TestAutoscalingGroupTagEnricher_NotFound(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{fetch: func(context.Context, *autoscaling.Client, string, string) (string, bool, bool, error) {
		return "", false, false, nil
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"autoscaling_group_name": "asg", "key": "k"},
		},
	}, EnrichClients{AutoScaling: &autoscaling.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAutoscalingGroupTagEnricher_ValidationErrorMapsToNotFound(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{fetch: func(context.Context, *autoscaling.Client, string, string) (string, bool, bool, error) {
		return "", false, false, &fakeASGTagSmithyErr{code: "ValidationError"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"autoscaling_group_name": "asg", "key": "k"},
		},
	}, EnrichClients{AutoScaling: &autoscaling.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAutoscalingGroupTagEnricher_UnexpectedErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("Throttling")
	enr := autoscalingGroupTagEnricher{fetch: func(context.Context, *autoscaling.Client, string, string) (string, bool, bool, error) {
		return "", false, false, want
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"autoscaling_group_name": "asg", "key": "k"},
		},
	}, EnrichClients{AutoScaling: &autoscaling.Client{}})
	require.ErrorIs(t, err, want)
}

func TestAutoscalingGroupTagEnricher_EnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{fetch: func(_ context.Context, _ *autoscaling.Client, asg, key string) (string, bool, bool, error) {
		assert.Equal(t, "asg-1", asg)
		assert.Equal(t, "Owner", key)
		return "team", false, true, nil
	}}
	id := &imported.ResourceIdentity{
		NativeIDs: map[string]string{
			"autoscaling_group_name": "asg-1",
			"key":                    "Owner",
		},
	}
	raw, err := enr.EnrichByID(context.Background(), id, EnrichClients{AutoScaling: &autoscaling.Client{}})
	require.NoError(t, err)
	var got generated.AWSAutoscalingGroupTag
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.AutoscalingGroupName)
	assert.Equal(t, "asg-1", *got.AutoscalingGroupName.Literal)
	require.Len(t, got.Tag, 1)
	require.NotNil(t, got.Tag[0].Value)
	assert.Equal(t, "team", *got.Tag[0].Value.Literal)
	require.NotNil(t, got.Tag[0].PropagateAtLaunch)
	assert.Equal(t, false, *got.Tag[0].PropagateAtLaunch.Literal)
}

func TestAutoscalingGroupTagEnricher_EnrichByID_NilClient(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		ImportID: "asg,k",
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestAutoscalingGroupTagEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := autoscalingGroupTagEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{AutoScaling: &autoscaling.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestAutoscalingGroupTagParts_ImportIDFallback(t *testing.T) {
	t.Parallel()
	asg, key, err := autoscalingGroupTagParts(&imported.ResourceIdentity{
		ImportID: "my-asg,Environment",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-asg", asg)
	assert.Equal(t, "Environment", key)
}

func TestAutoscalingGroupTagParts_NativeIDsWin(t *testing.T) {
	t.Parallel()
	asg, key, err := autoscalingGroupTagParts(&imported.ResourceIdentity{
		NativeIDs: map[string]string{"autoscaling_group_name": "native-asg", "key": "native-key"},
		ImportID:  "ignored,ignored",
	})
	require.NoError(t, err)
	assert.Equal(t, "native-asg", asg)
	assert.Equal(t, "native-key", key)
}

func TestAutoscalingGroupTagParts_MalformedImportID(t *testing.T) {
	t.Parallel()
	_, _, err := autoscalingGroupTagParts(&imported.ResourceIdentity{
		ImportID: "no-separator",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve")
}
