package metrics

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewClientsFromConfig_PreservesResolvedCredentials covers the
// motivating use case: a caller (reliable's Oracle credential broker,
// integration tests building configs via STS AssumeRole) hands in an
// already-resolved aws.Config, and the resulting Clients carries that
// exact config — no re-resolution against ambient defaults.
func TestNewClientsFromConfig_PreservesResolvedCredentials(t *testing.T) {
	t.Parallel()

	provider := credentials.NewStaticCredentialsProvider("AKIATESTKEYID", "test-secret", "test-token")
	cfg := aws.Config{
		Region:      "eu-west-2",
		Credentials: provider,
	}

	c, err := NewClientsFromConfig(cfg)
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.Equal(t, "eu-west-2", c.Region)
	require.NotNil(t, c.CloudWatch)
	assert.NotNil(t, c.baseCfg.Credentials, "baseCfg credentials must flow through for the lazy CloudFront client")
	assert.Equal(t, "eu-west-2", c.baseCfg.Region)
}

func TestNewClientsFromConfig_RejectsEmptyRegion(t *testing.T) {
	t.Parallel()

	cfg := aws.Config{Credentials: credentials.NewStaticCredentialsProvider("k", "s", "")}

	_, err := NewClientsFromConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cfg.Region is required")
}
