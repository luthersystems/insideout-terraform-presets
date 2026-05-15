package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// newTestSecretsManagerSecretEnricher builds a secretsmanagerSecretEnricher
// with a fake DescribeSecret fetch wired in. Mirrors the
// newTestDynamoDBEnricher pattern so the two test files read alike.
func newTestSecretsManagerSecretEnricher(
	describe func(ctx context.Context, c *secretsmanager.Client, id string) (*secretsmanager.DescribeSecretOutput, error),
) secretsmanagerSecretEnricher {
	return secretsmanagerSecretEnricher{fetch: describe}
}

// decodeSecretsmanagerAttrs round-trips ir.Attrs through UnmarshalAttrs
// and returns the typed AWSSecretsmanagerSecret. Mirrors the GCP-side
// decoded-typed assertion pattern.
func decodeSecretsmanagerAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSSecretsmanagerSecret {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_secretsmanager_secret", ir.Attrs)
	require.NoError(t, err)
	sec, ok := decoded.(*generated.AWSSecretsmanagerSecret)
	require.True(t, ok, "decoded type must be *AWSSecretsmanagerSecret, got %T", decoded)
	return sec
}

// decodeSecretsmanagerRaw is the EnrichByID counterpart: the helper
// returns json.RawMessage directly (no IR mutation), so the round-trip
// goes through UnmarshalAttrs on the raw bytes.
func decodeSecretsmanagerRaw(t *testing.T, raw json.RawMessage) *generated.AWSSecretsmanagerSecret {
	t.Helper()
	require.NotEmpty(t, raw, "EnrichByID must return a non-empty payload")
	decoded, err := generated.UnmarshalAttrs("aws_secretsmanager_secret", raw)
	require.NoError(t, err)
	sec, ok := decoded.(*generated.AWSSecretsmanagerSecret)
	require.True(t, ok, "decoded type must be *AWSSecretsmanagerSecret, got %T", decoded)
	return sec
}

func TestSecretsmanagerSecretEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newSecretsManagerSecretEnricher()
	assert.Equal(t, "aws_secretsmanager_secret", enr.ResourceType())
}

func TestSecretsmanagerSecretEnricher_ImplementsByIDEnricher(t *testing.T) {
	t.Parallel()
	// Compile-time guarantee that the production constructor returns
	// something satisfying both interfaces. Phase 2 contract: every new
	// enricher must implement ByIDEnricher.
	var _ AttributeEnricher = newSecretsManagerSecretEnricher()
	enr := newSecretsManagerSecretEnricher()
	_, ok := enr.(ByIDEnricher)
	assert.True(t, ok, "secretsmanagerSecretEnricher must implement ByIDEnricher per Phase 2 contract")
}

func TestSecretsmanagerSecretEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := secretsmanagerSecretEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_secretsmanager_secret",
			ImportID: "arn:aws:secretsmanager:us-east-1:012345678901:secret:foo-AbCdEf",
			NameHint: "foo",
		},
	}, EnrichClients{SecretsManager: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestSecretsmanagerSecretEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := secretsmanagerSecretEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_secretsmanager_secret",
		ImportID: "arn:aws:secretsmanager:us-east-1:012345678901:secret:foo-AbCdEf",
	}, EnrichClients{SecretsManager: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestSecretsmanagerSecretEnricher_SecretIDDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		id    imported.ResourceIdentity
		want  string
	}{
		{
			label: "ImportID wins (typically the ARN from the discoverer)",
			id: imported.ResourceIdentity{
				ImportID:  "arn:aws:secretsmanager:us-east-1:012345678901:secret:foo-AbCdEf",
				NameHint:  "foo",
				NativeIDs: map[string]string{"name": "foo-native"},
			},
			want: "arn:aws:secretsmanager:us-east-1:012345678901:secret:foo-AbCdEf",
		},
		{
			label: "NameHint is fallback when ImportID missing",
			id: imported.ResourceIdentity{
				NameHint:  "from-name-hint",
				NativeIDs: map[string]string{"name": "from-native"},
			},
			want: "from-name-hint",
		},
		{
			label: "NativeIDs[name] is last resort",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"name": "from-native"},
			},
			want: "from-native",
		},
		{
			label: "empty everywhere → empty",
			id:    imported.ResourceIdentity{},
			want:  "",
		},
		{
			label: "nil identity → empty",
			id:    imported.ResourceIdentity{}, // sentinel below
			want:  "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, secretsmanagerSecretIDForEnrich(&tc.id))
		})
	}
	// Cover the nil-pointer guard explicitly.
	assert.Equal(t, "", secretsmanagerSecretIDForEnrich(nil), "nil identity must yield empty id")
}

func TestSecretsmanagerSecretEnricher_DescribeError_Propagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDenied")
	enr := newTestSecretsManagerSecretEnricher(
		func(context.Context, *secretsmanager.Client, string) (*secretsmanager.DescribeSecretOutput, error) {
			return nil, wantErr
		},
	)
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret", ImportID: "foo"},
	}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestSecretsmanagerSecretEnricher_NotFoundMapsToErrNotFound(t *testing.T) {
	t.Parallel()
	enr := newTestSecretsManagerSecretEnricher(
		func(context.Context, *secretsmanager.Client, string) (*secretsmanager.DescribeSecretOutput, error) {
			return nil, &smtypes.ResourceNotFoundException{Message: aws.String("Secrets Manager can't find the specified secret.")}
		},
	)
	t.Run("Enrich", func(t *testing.T) {
		t.Parallel()
		err := enr.Enrich(context.Background(), &imported.ImportedResource{
			Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret", ImportID: "missing"},
		}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound, "typed ResourceNotFoundException must map onto ErrNotFound")
	})
	t.Run("EnrichByID", func(t *testing.T) {
		t.Parallel()
		_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
			Type:     "aws_secretsmanager_secret",
			ImportID: "missing",
		}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestSecretsmanagerSecretEnricher_NoIDReturnsError(t *testing.T) {
	t.Parallel()
	enr := newSecretsManagerSecretEnricher()
	t.Run("Enrich", func(t *testing.T) {
		t.Parallel()
		err := enr.Enrich(context.Background(), &imported.ImportedResource{
			Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret"},
		}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "cannot derive secret id"))
	})
	t.Run("EnrichByID", func(t *testing.T) {
		t.Parallel()
		byID, ok := enr.(ByIDEnricher)
		require.True(t, ok)
		_, err := byID.EnrichByID(context.Background(), &imported.ResourceIdentity{
			Type: "aws_secretsmanager_secret",
		}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "cannot derive secret id"))
	})
	t.Run("EnrichByID_NilIdentity", func(t *testing.T) {
		t.Parallel()
		byID, ok := enr.(ByIDEnricher)
		require.True(t, ok)
		_, err := byID.EnrichByID(context.Background(), nil,
			EnrichClients{SecretsManager: &secretsmanager.Client{}})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "nil identity"))
	})
}

func TestSecretsmanagerSecretEnricher_BasicMapping(t *testing.T) {
	t.Parallel()
	const (
		secretARN  = "arn:aws:secretsmanager:us-east-1:012345678901:secret:db-creds-AbCdEf"
		secretName = "db-creds"
		kmsKey     = "arn:aws:kms:us-east-1:012345678901:key/abc"
	)
	out := &secretsmanager.DescribeSecretOutput{
		ARN:         aws.String(secretARN),
		Name:        aws.String(secretName),
		Description: aws.String("Production DB credentials"),
		KmsKeyId:    aws.String(kmsKey),
		Tags: []smtypes.Tag{
			{Key: aws.String("Env"), Value: aws.String("prod")},
			{Key: aws.String("Project"), Value: aws.String("payments")},
		},
	}
	enr := newTestSecretsManagerSecretEnricher(
		func(_ context.Context, _ *secretsmanager.Client, gotID string) (*secretsmanager.DescribeSecretOutput, error) {
			// Enrich must pass through ImportID (the ARN) as SecretId
			// per the documented preference order. Pinning here
			// prevents a regression where the helper accidentally falls
			// through to NameHint first and downstream consumers stop
			// resolving by ARN.
			assert.Equal(t, secretARN, gotID, "Enrich must hand DescribeSecret the ARN from ImportID")
			return out, nil
		},
	)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_secretsmanager_secret",
			Address:  "aws_secretsmanager_secret.db_creds",
			ImportID: secretARN,
			NameHint: secretName,
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{SecretsManager: &secretsmanager.Client{}}))

	// ARN promoted onto NativeIDs (mirrors DynamoDB TableArn pattern).
	assert.Equal(t, secretARN, ir.Identity.NativeIDs["arn"],
		"enricher must stamp ARN onto Identity.NativeIDs[arn]")

	sec := decodeSecretsmanagerAttrs(t, ir)
	require.NotNil(t, sec.Name)
	assert.Equal(t, secretName, *sec.Name.Literal)
	require.NotNil(t, sec.Description)
	assert.Equal(t, "Production DB credentials", *sec.Description.Literal)
	require.NotNil(t, sec.KMSKeyID)
	assert.Equal(t, kmsKey, *sec.KMSKeyID.Literal)

	require.NotNil(t, sec.Tags)
	require.NotNil(t, sec.Tags["Env"])
	assert.Equal(t, "prod", *sec.Tags["Env"].Literal)
	require.NotNil(t, sec.Tags["Project"])
	assert.Equal(t, "payments", *sec.Tags["Project"].Literal)

	// Computed-only / TF-input-only fields must not appear in the
	// payload — decision #5.
	assert.Nil(t, sec.ARN, "arn is Computed; the enricher must NOT emit it on the typed payload (lives on Identity.NativeIDs)")
	assert.Nil(t, sec.ID, "id is Computed; the enricher must not emit it")
	assert.Nil(t, sec.TagsAll, "tags_all is Computed; the enricher must not emit it")
	assert.Nil(t, sec.RecoveryWindowInDays, "recovery_window_in_days is delete-time-only; the enricher must not emit it")
	assert.Nil(t, sec.ForceOverwriteReplicaSecret, "force_overwrite_replica_secret is TF-input-only; the enricher must not emit it")
	assert.Nil(t, sec.NamePrefix, "name_prefix is TF-input-only; the enricher must not emit it")
	assert.Nil(t, sec.Policy, "policy lives behind GetResourcePolicy; the single-call enricher must not emit it")
	assert.Empty(t, sec.Replica, "replica must be absent when ReplicationStatus is empty")
}

func TestSecretsmanagerSecretEnricher_TagsAbsent(t *testing.T) {
	t.Parallel()
	out := &secretsmanager.DescribeSecretOutput{
		ARN:  aws.String("arn:aws:secretsmanager:us-east-1:012345678901:secret:plain-AbCdEf"),
		Name: aws.String("plain"),
	}
	enr := newTestSecretsManagerSecretEnricher(
		func(context.Context, *secretsmanager.Client, string) (*secretsmanager.DescribeSecretOutput, error) {
			return out, nil
		},
	)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret", ImportID: "plain"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{SecretsManager: &secretsmanager.Client{}}))
	sec := decodeSecretsmanagerAttrs(t, ir)
	assert.Empty(t, sec.Tags, "tags must be absent when the API returns no tags (decision #34: no empty maps)")
}

func TestSecretsmanagerSecretEnricher_DefaultKMSKeyOmitsField(t *testing.T) {
	t.Parallel()
	// DescribeSecret returns KmsKeyId == "" when the secret uses the
	// AWS-managed default key. Emitting "" or
	// "alias/aws/secretsmanager" would diff against TF state where the
	// field is left unset.
	out := &secretsmanager.DescribeSecretOutput{
		ARN:      aws.String("arn:aws:secretsmanager:us-east-1:012345678901:secret:default-key-AbCdEf"),
		Name:     aws.String("default-key"),
		KmsKeyId: aws.String(""),
	}
	enr := newTestSecretsManagerSecretEnricher(
		func(context.Context, *secretsmanager.Client, string) (*secretsmanager.DescribeSecretOutput, error) {
			return out, nil
		},
	)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret", ImportID: "default-key"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{SecretsManager: &secretsmanager.Client{}}))
	sec := decodeSecretsmanagerAttrs(t, ir)
	assert.Nil(t, sec.KMSKeyID, "kms_key_id must be absent when the API reports the AWS-managed default key")
}

func TestSecretsmanagerSecretEnricher_ReplicaBlocks(t *testing.T) {
	t.Parallel()
	lastAccessed := time.Date(2025, 3, 14, 15, 9, 26, 0, time.UTC)
	out := &secretsmanager.DescribeSecretOutput{
		ARN:  aws.String("arn:aws:secretsmanager:us-east-1:012345678901:secret:multi-region-AbCdEf"),
		Name: aws.String("multi-region"),
		ReplicationStatus: []smtypes.ReplicationStatusType{
			{
				Region:           aws.String("us-west-2"),
				KmsKeyId:         aws.String("arn:aws:kms:us-west-2:012345678901:key/west"),
				Status:           smtypes.StatusTypeInSync,
				LastAccessedDate: &lastAccessed,
			},
			{
				Region:        aws.String("eu-west-1"),
				Status:        smtypes.StatusTypeInProgress,
				StatusMessage: aws.String("Replicating"),
			},
		},
	}
	enr := newTestSecretsManagerSecretEnricher(
		func(context.Context, *secretsmanager.Client, string) (*secretsmanager.DescribeSecretOutput, error) {
			return out, nil
		},
	)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret", ImportID: "multi-region"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{SecretsManager: &secretsmanager.Client{}}))
	sec := decodeSecretsmanagerAttrs(t, ir)
	require.Len(t, sec.Replica, 2)

	require.NotNil(t, sec.Replica[0].Region)
	assert.Equal(t, "us-west-2", *sec.Replica[0].Region.Literal)
	require.NotNil(t, sec.Replica[0].KMSKeyID)
	assert.Equal(t, "arn:aws:kms:us-west-2:012345678901:key/west", *sec.Replica[0].KMSKeyID.Literal)
	require.NotNil(t, sec.Replica[0].Status)
	assert.Equal(t, string(smtypes.StatusTypeInSync), *sec.Replica[0].Status.Literal)
	require.NotNil(t, sec.Replica[0].LastAccessedDate)
	assert.Equal(t, "2025-03-14T15:09:26Z", *sec.Replica[0].LastAccessedDate.Literal)
	assert.Nil(t, sec.Replica[0].StatusMessage, "status_message must be absent when the API omits it")

	require.NotNil(t, sec.Replica[1].Region)
	assert.Equal(t, "eu-west-1", *sec.Replica[1].Region.Literal)
	assert.Nil(t, sec.Replica[1].KMSKeyID, "kms_key_id must be absent for in-progress replica with no key reported")
	require.NotNil(t, sec.Replica[1].Status)
	assert.Equal(t, string(smtypes.StatusTypeInProgress), *sec.Replica[1].Status.Literal)
	require.NotNil(t, sec.Replica[1].StatusMessage)
	assert.Equal(t, "Replicating", *sec.Replica[1].StatusMessage.Literal)
}

func TestSecretsmanagerSecretEnricher_EnrichByID_BasicMapping(t *testing.T) {
	t.Parallel()
	const secretARN = "arn:aws:secretsmanager:us-east-1:012345678901:secret:db-creds-AbCdEf"
	out := &secretsmanager.DescribeSecretOutput{
		ARN:         aws.String(secretARN),
		Name:        aws.String("db-creds"),
		Description: aws.String("Production DB credentials"),
	}
	enr := newTestSecretsManagerSecretEnricher(
		func(_ context.Context, _ *secretsmanager.Client, gotID string) (*secretsmanager.DescribeSecretOutput, error) {
			assert.Equal(t, secretARN, gotID)
			return out, nil
		},
	)
	byID, ok := AttributeEnricher(enr).(ByIDEnricher)
	require.True(t, ok)
	identity := &imported.ResourceIdentity{
		Type:     "aws_secretsmanager_secret",
		ImportID: secretARN,
	}
	raw, err := byID.EnrichByID(context.Background(), identity,
		EnrichClients{SecretsManager: &secretsmanager.Client{}})
	require.NoError(t, err)
	sec := decodeSecretsmanagerRaw(t, raw)
	require.NotNil(t, sec.Name)
	assert.Equal(t, "db-creds", *sec.Name.Literal)
	require.NotNil(t, sec.Description)
	assert.Equal(t, "Production DB credentials", *sec.Description.Literal)

	// EnrichByID must NOT mutate the caller's identity — the per-IR
	// drift refresh path expects identity to be authoritative input.
	assert.Empty(t, identity.NativeIDs,
		"EnrichByID must not stamp NativeIDs onto the caller's identity")
}

func TestSecretsmanagerSecretEnricher_EnrichByID_DescribeError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDenied")
	enr := newTestSecretsManagerSecretEnricher(
		func(context.Context, *secretsmanager.Client, string) (*secretsmanager.DescribeSecretOutput, error) {
			return nil, wantErr
		},
	)
	byID, ok := AttributeEnricher(enr).(ByIDEnricher)
	require.True(t, ok)
	_, err := byID.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_secretsmanager_secret",
		ImportID: "foo",
	}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestSecretsmanagerSecretEnricher_NilDescribeOutputErrors(t *testing.T) {
	t.Parallel()
	// Defense-in-depth: a fetch hook returning (nil, nil) (e.g. a
	// future overlay-source helper that bails out silently) must not
	// crash the enricher — it should surface a clear error.
	enr := newTestSecretsManagerSecretEnricher(
		func(context.Context, *secretsmanager.Client, string) (*secretsmanager.DescribeSecretOutput, error) {
			return nil, nil
		},
	)
	t.Run("Enrich", func(t *testing.T) {
		t.Parallel()
		err := enr.Enrich(context.Background(), &imported.ImportedResource{
			Identity: imported.ResourceIdentity{Type: "aws_secretsmanager_secret", ImportID: "x"},
		}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "empty response"))
	})
	t.Run("EnrichByID", func(t *testing.T) {
		t.Parallel()
		byID, ok := AttributeEnricher(enr).(ByIDEnricher)
		require.True(t, ok)
		_, err := byID.EnrichByID(context.Background(), &imported.ResourceIdentity{
			Type:     "aws_secretsmanager_secret",
			ImportID: "x",
		}, EnrichClients{SecretsManager: &secretsmanager.Client{}})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "empty response"))
	})
}
