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

// TestMapPubsubTopic_Minimal pins the smallest non-trivial mapping:
// a topic with only the API-required Name field populated. The TF
// `name` attribute must hold the short form (the provider strips the
// projects/<p>/topics/ prefix on state reads), and the TF `project`
// attribute must come from the projectID argument since the API
// embeds the project only inside Name.
func TestMapPubsubTopic_Minimal(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name: "projects/my-real-project/topics/orders",
	}
	got := mapPubsubTopic(src, "my-real-project")

	require.NotNil(t, got.Name)
	assert.Equal(t, "orders", *got.Name.Literal,
		"name must hold the short topic name; the projects/<p>/topics/ prefix is the API form, not TF state")
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-real-project", *got.Project.Literal)

	// Optional scalars are nil when API leaves them empty.
	assert.Nil(t, got.KMSKeyName, "kms_key_name omitted on basic topic")
	assert.Nil(t, got.MessageRetentionDuration, "message_retention_duration omitted on basic topic")
	assert.Nil(t, got.Labels)

	// Nested blocks must remain unset on a basic topic.
	assert.Empty(t, got.IngestionDataSourceSettings)
	assert.Empty(t, got.MessageStoragePolicy)
	assert.Empty(t, got.SchemaSettings)

	// Computed-only / TF-only sentinel fields must not be populated.
	assert.Nil(t, got.ID, "id is computed-only")
	assert.Nil(t, got.EffectiveLabels, "effective_labels is computed-only")
	assert.Nil(t, got.TerraformLabels, "terraform_labels is computed-only")
	assert.Nil(t, got.Timeouts, "timeouts is a TF-only sentinel")
}

// TestMapPubsubTopic_NameExtractsShortForm covers the override that
// strips the projects/<p>/topics/ prefix. Tested independently of the
// happy path because the fallback ("no slash" → input passthrough) is
// defensive and easy to break with naive index math.
func TestMapPubsubTopic_NameExtractsShortForm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want string
	}{
		{"projects/proj/topics/t1", "t1"},
		{"projects/proj-with-dashes/topics/topic.with.dots", "topic.with.dots"},
		{"t-bare", "t-bare"}, // defensive fallback when no "/" present
		{"", ""},             // empty stays empty
	} {
		t.Run(tc.in, func(t *testing.T) {
			got := mapPubsubTopic(&pubsubv1.Topic{Name: tc.in}, "")
			require.NotNil(t, got.Name)
			assert.Equal(t, tc.want, *got.Name.Literal)
		})
	}
}

// TestMapPubsubTopic_LabelsStripGoogManaged verifies the goog-* /
// goog_* label filter. Same rationale as storage_bucket: the provider
// strips system labels in its read path, so we must too.
func TestMapPubsubTopic_LabelsStripGoogManaged(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name: "projects/p/topics/t",
		Labels: map[string]string{
			"environment":      "staging",
			"goog-managed":     "true",
			"goog_other_thing": "x",
			"team":             "platform",
		},
	}
	got := mapPubsubTopic(src, "")

	require.NotNil(t, got.Labels)
	assert.Len(t, got.Labels, 2, "goog-/goog_ system labels must be filtered")
	require.Contains(t, got.Labels, "environment")
	assert.Equal(t, "staging", *got.Labels["environment"].Literal)
	require.Contains(t, got.Labels, "team")
	assert.NotContains(t, got.Labels, "goog-managed")
	assert.NotContains(t, got.Labels, "goog_other_thing")
}

// TestMapPubsubTopic_LabelsAllSystemFiltered pins that an all-system-
// label topic produces a nil Labels map, not an empty one — so the
// emitter omits `labels = {}` entirely (decision #34: emitting an
// empty labels map would diff against state on next plan).
func TestMapPubsubTopic_LabelsAllSystemFiltered(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name:   "projects/p/topics/t",
		Labels: map[string]string{"goog-managed-by": "iam"},
	}
	got := mapPubsubTopic(src, "")
	assert.Nil(t, got.Labels, "all-system-label input must produce nil Labels (not empty map)")
}

// TestMapPubsubTopic_MessageStoragePolicy covers the simplest nested
// block: a singleton API pointer mapped into a TF block slice with
// the allowed_persistence_regions list lifted through
// stringSliceToValues.
func TestMapPubsubTopic_MessageStoragePolicy(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name: "projects/p/topics/t",
		MessageStoragePolicy: &pubsubv1.MessageStoragePolicy{
			AllowedPersistenceRegions: []string{"us-central1", "us-east1"},
		},
	}
	got := mapPubsubTopic(src, "")

	require.Len(t, got.MessageStoragePolicy, 1)
	pol := got.MessageStoragePolicy[0]
	require.Len(t, pol.AllowedPersistenceRegions, 2)
	assert.Equal(t, "us-central1", *pol.AllowedPersistenceRegions[0].Literal)
	assert.Equal(t, "us-east1", *pol.AllowedPersistenceRegions[1].Literal)
}

// TestMapPubsubTopic_SchemaSettings covers a singleton nested block
// carrying two scalars. Verifies both are picked up by the default
// reflection-driven mapping (no overrides needed for this block).
func TestMapPubsubTopic_SchemaSettings(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name: "projects/p/topics/t",
		SchemaSettings: &pubsubv1.SchemaSettings{
			Schema:   "projects/p/schemas/s1",
			Encoding: "JSON",
		},
	}
	got := mapPubsubTopic(src, "")

	require.Len(t, got.SchemaSettings, 1)
	ss := got.SchemaSettings[0]
	require.NotNil(t, ss.Schema)
	assert.Equal(t, "projects/p/schemas/s1", *ss.Schema.Literal)
	require.NotNil(t, ss.Encoding)
	assert.Equal(t, "JSON", *ss.Encoding.Literal)
}

// TestMapPubsubTopic_IngestionAWSKinesis covers a deeply-nested
// block whose API and TF field names differ via the snakeToCamel
// initialism handling (AWSKinesis ↔ aws_kinesis, AWSRoleARN ↔
// aws_role_arn, etc.). If the engine's initialism table regresses,
// this test catches it.
func TestMapPubsubTopic_IngestionAWSKinesis(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name: "projects/p/topics/t",
		IngestionDataSourceSettings: &pubsubv1.IngestionDataSourceSettings{
			AwsKinesis: &pubsubv1.AwsKinesis{
				AwsRoleArn:        "arn:aws:iam::123:role/r",
				GcpServiceAccount: "sa@p.iam.gserviceaccount.com",
				StreamArn:         "arn:aws:kinesis:us-east-1:123:stream/s",
				ConsumerArn:       "arn:aws:kinesis:us-east-1:123:stream/s/consumer/c:1",
			},
		},
	}
	got := mapPubsubTopic(src, "")

	require.Len(t, got.IngestionDataSourceSettings, 1)
	settings := got.IngestionDataSourceSettings[0]
	require.Len(t, settings.AWSKinesis, 1)
	k := settings.AWSKinesis[0]
	require.NotNil(t, k.AWSRoleARN)
	assert.Equal(t, "arn:aws:iam::123:role/r", *k.AWSRoleARN.Literal)
	require.NotNil(t, k.GCPServiceAccount)
	assert.Equal(t, "sa@p.iam.gserviceaccount.com", *k.GCPServiceAccount.Literal)
	require.NotNil(t, k.StreamARN)
	assert.Equal(t, "arn:aws:kinesis:us-east-1:123:stream/s", *k.StreamARN.Literal)
	require.NotNil(t, k.ConsumerARN)
	assert.Equal(t, "arn:aws:kinesis:us-east-1:123:stream/s/consumer/c:1", *k.ConsumerARN.Literal)
}

// TestMapPubsubTopic_IngestionCloudStorage covers the CloudStorage
// branch with one of the format selectors (TextFormat). Verifies the
// per-format empty-struct emission gates on API non-nil — emit the
// block when the user has it set, otherwise leave it off.
func TestMapPubsubTopic_IngestionCloudStorage(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name: "projects/p/topics/t",
		IngestionDataSourceSettings: &pubsubv1.IngestionDataSourceSettings{
			CloudStorage: &pubsubv1.CloudStorage{
				Bucket:    "io-ingest",
				MatchGlob: "logs/*.txt",
				TextFormat: &pubsubv1.TextFormat{
					Delimiter: "\n",
				},
			},
		},
	}
	got := mapPubsubTopic(src, "")

	require.Len(t, got.IngestionDataSourceSettings, 1)
	cs := got.IngestionDataSourceSettings[0].CloudStorage
	require.Len(t, cs, 1)
	require.NotNil(t, cs[0].Bucket)
	assert.Equal(t, "io-ingest", *cs[0].Bucket.Literal)
	require.NotNil(t, cs[0].MatchGlob)
	assert.Equal(t, "logs/*.txt", *cs[0].MatchGlob.Literal)
	require.Len(t, cs[0].TextFormat, 1)
	require.NotNil(t, cs[0].TextFormat[0].Delimiter)
	assert.Equal(t, "\n", *cs[0].TextFormat[0].Delimiter.Literal)

	assert.Empty(t, cs[0].AvroFormat, "avro_format must remain off when API leaves it nil")
	assert.Empty(t, cs[0].PubsubAvroFormat, "pubsub_avro_format must remain off when API leaves it nil")
}

// TestMapPubsubTopic_IngestionEmptyFormats covers the AvroFormat /
// PubSubAvroFormat empty-struct shape. When the API reports one of
// these formats present (non-nil empty struct), the emitter must
// produce a corresponding empty block on the TF side so the user's
// `avro_format {}` HCL choice round-trips.
func TestMapPubsubTopic_IngestionEmptyFormats(t *testing.T) {
	t.Parallel()
	src := &pubsubv1.Topic{
		Name: "projects/p/topics/t",
		IngestionDataSourceSettings: &pubsubv1.IngestionDataSourceSettings{
			CloudStorage: &pubsubv1.CloudStorage{
				Bucket:     "io-ingest",
				AvroFormat: &pubsubv1.AvroFormat{},
			},
		},
	}
	got := mapPubsubTopic(src, "")

	require.Len(t, got.IngestionDataSourceSettings, 1)
	cs := got.IngestionDataSourceSettings[0].CloudStorage
	require.Len(t, cs, 1)
	require.Len(t, cs[0].AvroFormat, 1, "empty avro_format struct must emit as an empty block")
	assert.Empty(t, cs[0].PubsubAvroFormat)
	assert.Empty(t, cs[0].TextFormat)
}

// TestPubsubTopicEnrich_ClientUnavailable pins that a nil
// EnrichClients.Pubsub returns the sentinel error so the aggregator
// can downgrade to a warn rather than batch-fail.
func TestPubsubTopicEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newPubsubTopicEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_pubsub_topic",
			ImportID: "projects/p/topics/t",
			Address:  "google_pubsub_topic.t",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Pubsub: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

// TestPubsubTopicEnrich_NameMissing pins that an enricher invoked
// against a resource with no derivable full resource name fails fast
// with a useful error rather than calling Get("") and getting a
// less-actionable API rejection.
func TestPubsubTopicEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := pubsubTopicEnricher{
		fetch: func(_ context.Context, _ *pubsubv1.Service, _ string) (*pubsubv1.Topic, error) {
			t.Fatal("fetch must not be called when full name unknown")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:    "google_pubsub_topic",
			Address: "google_pubsub_topic.t",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Pubsub: &pubsubv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive topic resource name")
}

// TestPubsubTopicEnrich_NameFromAssetFallback pins that when ImportID
// is empty but NativeIDs["asset_name"] + ProjectID are present, the
// enricher reconstructs the fully-qualified name correctly. This is
// the safety-net path for any future code path that populates
// Identity exclusively from CAI without an ImportID.
func TestPubsubTopicEnrich_NameFromAssetFallback(t *testing.T) {
	t.Parallel()
	var gotName string
	e := pubsubTopicEnricher{
		fetch: func(_ context.Context, _ *pubsubv1.Service, name string) (*pubsubv1.Topic, error) {
			gotName = name
			return &pubsubv1.Topic{Name: "projects/my-proj/topics/orders"}, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:    "google_pubsub_topic",
			Address: "google_pubsub_topic.orders",
			NativeIDs: map[string]string{
				"asset_name": "//pubsub.googleapis.com/projects/my-proj/topics/orders",
			},
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Pubsub: &pubsubv1.Service{}, ProjectID: "my-proj"}))
	assert.Equal(t, "projects/my-proj/topics/orders", gotName,
		"fallback must reconstruct projects/<p>/topics/<n> from NativeIDs asset_name + ProjectID")
}

// TestPubsubTopicEnrich_FetchError pins that a real API failure is
// wrapped with the full resource name for actionable debugging.
func TestPubsubTopicEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 Forbidden: insufficient permission")
	e := pubsubTopicEnricher{
		fetch: func(_ context.Context, _ *pubsubv1.Service, _ string) (*pubsubv1.Topic, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_pubsub_topic",
			ImportID: "projects/p/topics/orders",
			Address:  "google_pubsub_topic.orders",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Pubsub: &pubsubv1.Service{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "projects/p/topics/orders", "error must name the failing topic")
}

// TestPubsubTopicEnrich_PopulatesAttrs is the end-to-end happy path:
// a fake fetcher returns a topic, the enricher writes the typed
// payload into ir.Attrs, and the JSON round-trips cleanly through
// generated.UnmarshalAttrs.
func TestPubsubTopicEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	topic := &pubsubv1.Topic{
		Name:                     "projects/my-project/topics/orders",
		KmsKeyName:               "projects/my-project/locations/us/keyRings/k/cryptoKeys/c",
		MessageRetentionDuration: "604800s",
		Labels:                   map[string]string{"environment": "production"},
		MessageStoragePolicy: &pubsubv1.MessageStoragePolicy{
			AllowedPersistenceRegions: []string{"us-central1"},
		},
	}
	e := pubsubTopicEnricher{
		fetch: func(_ context.Context, _ *pubsubv1.Service, name string) (*pubsubv1.Topic, error) {
			assert.Equal(t, "projects/my-project/topics/orders", name,
				"fetch must be called with the fully-qualified resource name")
			return topic, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_pubsub_topic",
			ImportID: "projects/my-project/topics/orders",
			Address:  "google_pubsub_topic.orders",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{
		Pubsub: &pubsubv1.Service{}, ProjectID: "my-project",
	}))

	require.NotEmpty(t, ir.Attrs)
	decoded, err := generated.UnmarshalAttrs("google_pubsub_topic", ir.Attrs)
	require.NoError(t, err)
	gt, ok := decoded.(*generated.GooglePubsubTopic)
	require.True(t, ok, "decoded type must be *GooglePubsubTopic, got %T", decoded)
	require.NotNil(t, gt.Name)
	assert.Equal(t, "orders", *gt.Name.Literal)
	require.NotNil(t, gt.Project)
	assert.Equal(t, "my-project", *gt.Project.Literal)
	require.NotNil(t, gt.KMSKeyName)
	assert.Equal(t, topic.KmsKeyName, *gt.KMSKeyName.Literal)
	require.NotNil(t, gt.MessageRetentionDuration)
	assert.Equal(t, "604800s", *gt.MessageRetentionDuration.Literal)
	require.Len(t, gt.MessageStoragePolicy, 1)
}

// TestPubsubTopicEnrich_RoundTripThroughEmitImportedTF is the
// decision-#34 contract test for the typed-Attrs path on
// google_pubsub_topic. Same shape as the storage_bucket variant:
// emit must route through the typed branch, carry mapped values
// through to HCL, and emit nested blocks as block syntax (not map
// literals).
func TestPubsubTopicEnrich_RoundTripThroughEmitImportedTF(t *testing.T) {
	t.Parallel()
	topic := &pubsubv1.Topic{
		Name:                     "projects/my-project/topics/orders",
		KmsKeyName:               "projects/my-project/locations/us/keyRings/k/cryptoKeys/c",
		MessageRetentionDuration: "604800s",
		Labels:                   map[string]string{"environment": "production"},
		MessageStoragePolicy: &pubsubv1.MessageStoragePolicy{
			AllowedPersistenceRegions: []string{"us-central1", "us-east1"},
		},
		SchemaSettings: &pubsubv1.SchemaSettings{
			Schema:   "projects/my-project/schemas/order-schema",
			Encoding: "JSON",
		},
	}
	typed := mapPubsubTopic(topic, "my-project")
	raw, err := json.Marshal(typed)
	require.NoError(t, err)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     "google_pubsub_topic",
			Address:  "google_pubsub_topic.orders",
			ImportID: "projects/my-project/topics/orders",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: raw,
	}
	out, used := composer.EmitImportedTF("gcp", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.NotNil(t, out, "typed emit must succeed end-to-end")
	require.True(t, used["gcp"])
	s := string(out)

	// Resource and import blocks present.
	assert.Contains(t, s, `resource "google_pubsub_topic" "orders"`)
	assert.Contains(t, s, "to = google_pubsub_topic.orders")

	// Top-level scalars. hclwrite columnar-aligns `=` so be alignment-
	// insensitive in the regex.
	assert.Regexp(t, `(?m)^\s*name\s+=\s+"orders"`, s, "name must be the short form, not the fully-qualified API name")
	assert.Regexp(t, `(?m)^\s*project\s+=\s+"my-project"`, s, "project missing")
	assert.Regexp(t, `(?m)^\s*kms_key_name\s+=\s+"projects/my-project/locations/us/keyRings/k/cryptoKeys/c"`, s)
	assert.Regexp(t, `(?m)^\s*message_retention_duration\s+=\s+"604800s"`, s)

	// Nested blocks emitted as block syntax. message_storage_policy
	// is the load-bearing one — it carries a list scalar and must
	// emit as `message_storage_policy {`.
	assert.Regexp(t, `(?m)^\s*message_storage_policy\s*\{`, s, "message_storage_policy must emit as block syntax")
	assert.Contains(t, s, `"us-central1"`)
	assert.Contains(t, s, `"us-east1"`)
	assert.Regexp(t, `(?m)^\s*schema_settings\s*\{`, s, "schema_settings must emit as block syntax")
	assert.Contains(t, s, `schema   = "projects/my-project/schemas/order-schema"`)

	// Computed-only fields must not appear.
	for _, computed := range []string{"effective_labels", "terraform_labels"} {
		assert.NotContains(t, s, computed, "computed-only field %q must not appear", computed)
	}

	// Labels map sanity.
	assert.True(t,
		strings.Contains(s, `"environment" = "production"`) || strings.Contains(s, `environment = "production"`),
		"labels map must carry the production value: %s", s)
}

// TestPubsubTopicRegisteredOnAggregator pins that NewGCPDiscoverer
// registers the pubsub_topic enricher in byTypeEnricher, so a
// caller's EnrichAttributes call reaches the right enricher rather
// than silently skipping it.
func TestPubsubTopicRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_pubsub_topic"]
	require.True(t, ok, "google_pubsub_topic must be registered in byTypeEnricher")
	require.NotNil(t, enr)
	assert.Equal(t, "google_pubsub_topic", enr.ResourceType())
}
