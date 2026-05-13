package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestMapPubsubSubscription_Minimal pins the smallest non-trivial
// mapping. Required TF fields are name + topic; the API embeds the
// project in Name and stores `topic` as a fully-qualified resource
// name (the TF schema accepts that form, and that's what state holds).
func TestMapPubsubSubscription_Minimal(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Subscription{
		Name:  "projects/my-project/subscriptions/orders-sub",
		Topic: "projects/my-project/topics/orders",
	}
	got := mapPubsubSubscription(src, "my-project")

	require.NotNil(t, got.Name)
	assert.Equal(t, "orders-sub", *got.Name.Literal,
		"name must be the short form (the projects/<p>/subscriptions/ prefix is the API shape, not TF state)")
	require.NotNil(t, got.Topic)
	assert.Equal(t, "projects/my-project/topics/orders", *got.Topic.Literal,
		"topic must be the fully-qualified API form — that's what TF state stores")
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)

	// Bool fields without a pointer guard emit unconditionally with
	// the API value (zero=false here). Verifies the engine's default
	// behavior for plain-bool API + *Value[bool] TF.
	require.NotNil(t, got.RetainAckedMessages)
	assert.False(t, *got.RetainAckedMessages.Literal)
	require.NotNil(t, got.EnableMessageOrdering)
	assert.False(t, *got.EnableMessageOrdering.Literal)
	require.NotNil(t, got.EnableExactlyOnceDelivery)
	assert.False(t, *got.EnableExactlyOnceDelivery.Literal)

	// Computed-only / TF-only sentinel fields.
	assert.Nil(t, got.ID)
	assert.Nil(t, got.EffectiveLabels)
	assert.Nil(t, got.TerraformLabels)
	assert.Nil(t, got.Timeouts)

	// Empty nested blocks.
	assert.Empty(t, got.BigqueryConfig)
	assert.Empty(t, got.CloudStorageConfig)
	assert.Empty(t, got.DeadLetterPolicy)
	assert.Empty(t, got.ExpirationPolicy)
	assert.Empty(t, got.PushConfig)
	assert.Empty(t, got.RetryPolicy)
}

// TestMapPubsubSubscription_PushConfigWithOIDC covers the deeply-
// nested push_config with an OIDC token — exercises the OidcToken
// initialism alias (OIDCToken on TF, OidcToken on API).
func TestMapPubsubSubscription_PushConfigWithOIDC(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Subscription{
		Name:  "projects/p/subscriptions/s",
		Topic: "projects/p/topics/t",
		PushConfig: &pubsubv1.PushConfig{
			PushEndpoint: "https://example.com/webhook",
			Attributes:   map[string]string{"x-goog-version": "v1"},
			OidcToken: &pubsubv1.OidcToken{
				Audience:            "https://example.com",
				ServiceAccountEmail: "sa@p.iam.gserviceaccount.com",
			},
		},
	}
	got := mapPubsubSubscription(src, "p")

	require.Len(t, got.PushConfig, 1)
	pc := got.PushConfig[0]
	require.NotNil(t, pc.PushEndpoint)
	assert.Equal(t, "https://example.com/webhook", *pc.PushEndpoint.Literal)
	require.Contains(t, pc.Attributes, "x-goog-version")
	assert.Equal(t, "v1", *pc.Attributes["x-goog-version"].Literal)
	require.Len(t, pc.OIDCToken, 1)
	require.NotNil(t, pc.OIDCToken[0].Audience)
	assert.Equal(t, "https://example.com", *pc.OIDCToken[0].Audience.Literal)
	require.NotNil(t, pc.OIDCToken[0].ServiceAccountEmail)
	assert.Equal(t, "sa@p.iam.gserviceaccount.com", *pc.OIDCToken[0].ServiceAccountEmail.Literal)
}

// TestMapPubsubSubscription_BigQueryAndCloudStorageConfigs covers the
// two main pull-config alternatives.
func TestMapPubsubSubscription_BigQueryAndCloudStorageConfigs(t *testing.T) {
	t.Parallel()
	t.Run("bigquery", func(t *testing.T) {
		src := &pubsubv1.Subscription{
			Name:  "projects/p/subscriptions/s",
			Topic: "projects/p/topics/t",
			BigqueryConfig: &pubsubv1.BigQueryConfig{
				Table:               "p.dataset.table",
				UseTableSchema:      true,
				ServiceAccountEmail: "sa@p.iam.gserviceaccount.com",
			},
		}
		got := mapPubsubSubscription(src, "p")
		require.Len(t, got.BigqueryConfig, 1)
		require.NotNil(t, got.BigqueryConfig[0].Table)
		assert.Equal(t, "p.dataset.table", *got.BigqueryConfig[0].Table.Literal)
		require.NotNil(t, got.BigqueryConfig[0].UseTableSchema)
		assert.True(t, *got.BigqueryConfig[0].UseTableSchema.Literal)
	})

	t.Run("cloud_storage", func(t *testing.T) {
		src := &pubsubv1.Subscription{
			Name:  "projects/p/subscriptions/s",
			Topic: "projects/p/topics/t",
			CloudStorageConfig: &pubsubv1.CloudStorageConfig{
				Bucket:         "io-archive",
				FilenamePrefix: "events-",
				MaxBytes:       104857600,
				AvroConfig: &pubsubv1.AvroConfig{
					UseTopicSchema: true,
					WriteMetadata:  true,
				},
			},
		}
		got := mapPubsubSubscription(src, "p")
		require.Len(t, got.CloudStorageConfig, 1)
		cs := got.CloudStorageConfig[0]
		require.NotNil(t, cs.Bucket)
		assert.Equal(t, "io-archive", *cs.Bucket.Literal)
		require.NotNil(t, cs.FilenamePrefix)
		assert.Equal(t, "events-", *cs.FilenamePrefix.Literal)
		require.NotNil(t, cs.MaxBytes)
		assert.Equal(t, int64(104857600), *cs.MaxBytes.Literal)
		require.Len(t, cs.AvroConfig, 1)
		require.NotNil(t, cs.AvroConfig[0].UseTopicSchema)
		assert.True(t, *cs.AvroConfig[0].UseTopicSchema.Literal)
	})
}

// TestMapPubsubSubscription_DeadLetterRetryAndExpiration covers the
// three simpler singleton policy blocks together.
func TestMapPubsubSubscription_DeadLetterRetryAndExpiration(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Subscription{
		Name:  "projects/p/subscriptions/s",
		Topic: "projects/p/topics/t",
		DeadLetterPolicy: &pubsubv1.DeadLetterPolicy{
			DeadLetterTopic:     "projects/p/topics/dlq",
			MaxDeliveryAttempts: 7,
		},
		RetryPolicy: &pubsubv1.RetryPolicy{
			MaximumBackoff: "600s",
			MinimumBackoff: "10s",
		},
		ExpirationPolicy: &pubsubv1.ExpirationPolicy{Ttl: "2678400s"},
	}
	got := mapPubsubSubscription(src, "p")

	require.Len(t, got.DeadLetterPolicy, 1)
	require.NotNil(t, got.DeadLetterPolicy[0].DeadLetterTopic)
	assert.Equal(t, "projects/p/topics/dlq", *got.DeadLetterPolicy[0].DeadLetterTopic.Literal)
	require.NotNil(t, got.DeadLetterPolicy[0].MaxDeliveryAttempts)
	assert.Equal(t, int64(7), *got.DeadLetterPolicy[0].MaxDeliveryAttempts.Literal)

	require.Len(t, got.RetryPolicy, 1)
	require.NotNil(t, got.RetryPolicy[0].MaximumBackoff)
	assert.Equal(t, "600s", *got.RetryPolicy[0].MaximumBackoff.Literal)
	require.NotNil(t, got.RetryPolicy[0].MinimumBackoff)
	assert.Equal(t, "10s", *got.RetryPolicy[0].MinimumBackoff.Literal)

	require.Len(t, got.ExpirationPolicy, 1)
	require.NotNil(t, got.ExpirationPolicy[0].TTL)
	assert.Equal(t, "2678400s", *got.ExpirationPolicy[0].TTL.Literal)
}

// TestPubsubSubscriptionEnrich_ClientUnavailable + …NameMissing +
// …FetchError mirror the contract tests from pubsub_topic_enrich_test.go.
func TestPubsubSubscriptionEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newPubsubSubscriptionEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_pubsub_subscription", ImportID: "projects/p/subscriptions/s", Address: "google_pubsub_subscription.s",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Pubsub: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestPubsubSubscriptionEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := pubsubSubscriptionEnricher{
		fetch: func(_ context.Context, _ *pubsubv1.Service, _ string) (*pubsubv1.Subscription, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_pubsub_subscription"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Pubsub: &pubsubv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive subscription resource name")
}

func TestPubsubSubscriptionEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("404 Subscription not found")
	e := pubsubSubscriptionEnricher{
		fetch: func(_ context.Context, _ *pubsubv1.Service, _ string) (*pubsubv1.Subscription, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_pubsub_subscription",
			ImportID: "projects/p/subscriptions/missing-sub",
			Address:  "google_pubsub_subscription.missing",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Pubsub: &pubsubv1.Service{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "missing-sub")
}

// TestPubsubSubscriptionEnrich_PopulatesAttrs is the end-to-end
// happy path: fake fetch returns a subscription, enricher writes to
// ir.Attrs, JSON round-trips through generated.UnmarshalAttrs.
func TestPubsubSubscriptionEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Subscription{
		Name:                     "projects/p/subscriptions/orders-sub",
		Topic:                    "projects/p/topics/orders",
		AckDeadlineSeconds:       20,
		MessageRetentionDuration: "604800s",
		Labels:                   map[string]string{"environment": "prod"},
	}
	e := pubsubSubscriptionEnricher{
		fetch: func(_ context.Context, _ *pubsubv1.Service, name string) (*pubsubv1.Subscription, error) {
			assert.Equal(t, "projects/p/subscriptions/orders-sub", name)
			return src, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_pubsub_subscription",
			ImportID: "projects/p/subscriptions/orders-sub",
			Address:  "google_pubsub_subscription.orders",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Pubsub: &pubsubv1.Service{}, ProjectID: "p"}))

	decoded, err := generated.UnmarshalAttrs("google_pubsub_subscription", ir.Attrs)
	require.NoError(t, err)
	gs, ok := decoded.(*generated.GooglePubsubSubscription)
	require.True(t, ok)
	require.NotNil(t, gs.Name)
	assert.Equal(t, "orders-sub", *gs.Name.Literal)
	require.NotNil(t, gs.AckDeadlineSeconds)
	assert.Equal(t, int64(20), *gs.AckDeadlineSeconds.Literal)
}

// TestPubsubSubscriptionEnrich_RoundTripThroughEmitImportedTF is the
// decision-#34 contract test.
func TestPubsubSubscriptionEnrich_RoundTripThroughEmitImportedTF(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Subscription{
		Name:               "projects/my-project/subscriptions/orders-sub",
		Topic:              "projects/my-project/topics/orders",
		AckDeadlineSeconds: 20,
		PushConfig: &pubsubv1.PushConfig{
			PushEndpoint: "https://example.com/webhook",
		},
		DeadLetterPolicy: &pubsubv1.DeadLetterPolicy{
			DeadLetterTopic:     "projects/my-project/topics/dlq",
			MaxDeliveryAttempts: 5,
		},
	}
	typed := mapPubsubSubscription(src, "my-project")
	raw, err := json.Marshal(typed)
	require.NoError(t, err)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "gcp", Type: "google_pubsub_subscription",
			Address: "google_pubsub_subscription.orders",
			ImportID: "projects/my-project/subscriptions/orders-sub",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: raw,
	}
	out, used := composer.EmitImportedTF("gcp", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.NotNil(t, out)
	require.True(t, used["gcp"])
	s := string(out)

	assert.Contains(t, s, `resource "google_pubsub_subscription" "orders"`)
	assert.Regexp(t, `(?m)^\s*name\s+=\s+"orders-sub"`, s)
	assert.Regexp(t, `(?m)^\s*topic\s+=\s+"projects/my-project/topics/orders"`, s)
	assert.Regexp(t, `(?m)^\s*ack_deadline_seconds\s+=\s+20`, s)

	// Nested blocks emit as block syntax.
	assert.Regexp(t, `(?m)^\s*push_config\s*\{`, s, "push_config must emit as block syntax")
	assert.Contains(t, s, `push_endpoint = "https://example.com/webhook"`)
	assert.Regexp(t, `(?m)^\s*dead_letter_policy\s*\{`, s, "dead_letter_policy must emit as block syntax")
	assert.Contains(t, s, `dead_letter_topic     = "projects/my-project/topics/dlq"`)

	// Computed-only fields must not appear.
	for _, computed := range []string{"effective_labels", "terraform_labels"} {
		assert.NotContains(t, s, computed)
	}

	// Sanity: no stray opaque-path markers.
	assert.NotContains(t, s, "push_config = {", "push_config must NOT emit as map literal (decision #34 regression marker)")
	_ = strings.Contains // keep import used in case earlier asserts get trimmed
}

func TestPubsubSubscriptionRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_pubsub_subscription"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_pubsub_subscription", enr.ResourceType())
}
