package composer

// union_test.go locks the fold-of-partial-records contract that the
// UnionConfig + UnionComponents helpers carry. Mirrors the regression suite
// the same rules were under on the reliable side (chatv2/stack_components.go's
// mergeConfig / mergeComponents families) so the upstream-migration in
// luthersystems/reliable#1437 PR-2 cannot regress on either side.
//
// Key invariants under test:
//
//   - Last-non-zero per leaf wins (later parts in the slice override earlier
//     parts where the later part has a non-zero value at that leaf).
//   - Pointer-to-false is non-zero — explicit-deselect must overwrite an
//     earlier explicit-select. This is the #1043 deselection canary.
//   - Sub-struct recursion: a later part that touches only AWSVPC.AZCount
//     must not clobber an earlier part's AWSVPC.EnableNATGateway.
//   - Allocate-on-demand: when dst's inner *struct is nil and src's isn't,
//     dst gets a FRESH inner struct — not src's pointer — so callers can't
//     accidentally mutate part values via the result.
//   - Union performs MERGE ONLY. Normalization (cross-cloud stripping, etc.)
//     is a separate concern handled by Components.Normalize / Config.Normalize
//     or by the caller's adapter — Union must not auto-strip.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────────────────────────────────
// UnionConfig
// ──────────────────────────────────────────────────────────────────────────

func TestUnionConfig_LastNonZeroWins(t *testing.T) {
	t.Parallel()

	parts := []Config{
		{Cloud: "AWS", Region: "us-east-1"},
		{Cloud: "GCP"},
	}
	got := UnionConfig(parts)

	assert.Equal(t, "GCP", got.Cloud, "later non-zero Cloud must override earlier")
	assert.Equal(t, "us-east-1", got.Region, "earlier Region must survive when later part is zero at that leaf")
}

func TestUnionConfig_EmptyPartsIsNoOp(t *testing.T) {
	t.Parallel()

	parts := []Config{
		{Cloud: "AWS", Region: "us-east-1"},
		{}, // entirely zero — contributes nothing
		{},
	}
	got := UnionConfig(parts)

	assert.Equal(t, "AWS", got.Cloud)
	assert.Equal(t, "us-east-1", got.Region)
}

func TestUnionConfig_PointerToFalsePreserved(t *testing.T) {
	t.Parallel()

	// Part 1 enables NAT; part 2 explicitly disables it. Result must be
	// the explicit-false pointer — the canary that we lock on
	// pointer-non-nil (not dereferenced value) for the IsZero predicate.
	parts := []Config{
		*cfgWithVPCSubBlock(nil, boolPtr(true), nil),
		*cfgWithVPCSubBlock(nil, boolPtr(false), nil),
	}
	got := UnionConfig(parts)

	require.NotNil(t, got.AWSVPC)
	require.NotNil(t, got.AWSVPC.EnableNATGateway,
		"explicit *bool(&false) must override an earlier *bool(&true)")
	assert.False(t, *got.AWSVPC.EnableNATGateway,
		"final dereferenced value must be the later part's false")
}

func TestUnionConfig_SubStructRecursion(t *testing.T) {
	t.Parallel()

	// Part 1 sets only EnableNATGateway. Part 2 sets only AZCount. Result
	// must have BOTH populated in a single AWSVPC sub-block — proves the
	// recursion descends into the inner struct rather than wholesale
	// replacing the *struct pointer.
	parts := []Config{
		*cfgWithVPCSubBlock(nil, boolPtr(true), nil),
		*cfgWithVPCSubBlock(nil, nil, intPtr(3)),
	}
	got := UnionConfig(parts)

	require.NotNil(t, got.AWSVPC)
	require.NotNil(t, got.AWSVPC.EnableNATGateway, "earlier sub-field must survive")
	assert.True(t, *got.AWSVPC.EnableNATGateway)
	require.NotNil(t, got.AWSVPC.AZCount, "later sub-field must land")
	assert.Equal(t, 3, *got.AWSVPC.AZCount)
}

func TestUnionConfig_AllocatesFreshInnerStruct(t *testing.T) {
	t.Parallel()

	// Build a single-part fold. Mutate the result's inner *struct after
	// the call; the part's inner *struct must NOT be affected — proves dst
	// got a fresh inner struct rather than shared src's pointer.
	part := *cfgWithVPCSubBlock(nil, boolPtr(true), intPtr(2))
	require.NotNil(t, part.AWSVPC)
	originalAZCount := *part.AWSVPC.AZCount

	got := UnionConfig([]Config{part})
	require.NotNil(t, got.AWSVPC)

	// Pointer identity check at the *struct level — must differ.
	assert.NotSame(t, part.AWSVPC, got.AWSVPC,
		"result's inner *struct must be a fresh allocation, not the part's pointer")

	// Mutate the result's inner struct via a new scalar pointer field; the
	// part's AZCount pointer must be unchanged.
	got.AWSVPC.AZCount = intPtr(99)
	assert.Equal(t, originalAZCount, *part.AWSVPC.AZCount,
		"mutating result.AWSVPC.AZCount must not affect the part's AZCount")
}

// TestUnionConfig_ScalarPointersAreSharedAtLeaves locks the documented
// "Scalar-pointer fields INSIDE the struct are still shallow-copied" rule
// from union.go's docstring. Mutating the leaf value through the result's
// *int (e.g. *result.AWSVPC.AZCount = 99) DOES propagate back to the part
// — callers must not mutate parts after the call. If a future refactor
// silently switches to deep-clone, this test catches it. /qa-professor P2-4.
func TestUnionConfig_ScalarPointersAreSharedAtLeaves(t *testing.T) {
	t.Parallel()

	az := 3
	part := *cfgWithVPCSubBlock(nil, boolPtr(true), &az)
	got := UnionConfig([]Config{part})
	require.NotNil(t, got.AWSVPC)
	require.NotNil(t, got.AWSVPC.AZCount)

	// Dereference-mutate via the result's pointer. The part's *int MUST
	// see the change — that's the contract.
	*got.AWSVPC.AZCount = 99
	require.NotNil(t, part.AWSVPC.AZCount,
		"part's AZCount pointer must still be set after the union call")
	assert.Equal(t, 99, *part.AWSVPC.AZCount,
		"scalar *int pointers are shallow-copied at the leaf — mutating through the result "+
			"MUST propagate back to the part. Callers must not mutate parts post-call.")
	// And the result reflects the same mutation, of course.
	assert.Equal(t, 99, *got.AWSVPC.AZCount)
}

// TestUnionConfig_NonNilEmptySliceOverridesPopulated locks the documented
// rule: a non-nil empty slice is non-zero per reflect.Value.IsZero, so it
// overrides an earlier populated slice. The union.go docstring spells this
// out at the field-rule level — without this test, a future "smart"
// refactor that special-cases empty slices would silently change behaviour
// and the doc would drift. /qa-professor P2-5.
func TestUnionConfig_NonNilEmptySliceOverridesPopulated(t *testing.T) {
	t.Parallel()

	parts := []Config{
		{
			AWSEC2: &struct {
				InstanceType          string `json:"instanceType,omitempty"`
				NumServers            string `json:"numServers,omitempty"`
				NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
				DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
				UserData              string `json:"userData,omitempty"`
				UserDataURL           string `json:"userDataURL,omitempty"`
				CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
				SSHPublicKey          string `json:"sshPublicKey,omitempty"`
				EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
				GPUEnabled            *bool  `json:"gpuEnabled,omitempty"`
			}{CustomIngressPorts: []int{22, 80, 443}},
		},
		{
			AWSEC2: &struct {
				InstanceType          string `json:"instanceType,omitempty"`
				NumServers            string `json:"numServers,omitempty"`
				NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
				DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
				UserData              string `json:"userData,omitempty"`
				UserDataURL           string `json:"userDataURL,omitempty"`
				CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
				SSHPublicKey          string `json:"sshPublicKey,omitempty"`
				EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
				GPUEnabled            *bool  `json:"gpuEnabled,omitempty"`
			}{CustomIngressPorts: []int{}}, // non-nil empty — IS zero-distinct
		},
	}
	got := UnionConfig(parts)

	require.NotNil(t, got.AWSEC2)
	require.NotNil(t, got.AWSEC2.CustomIngressPorts,
		"non-nil empty slice in later part must override and survive (slice is not nil even though len==0)")
	assert.Equal(t, 0, len(got.AWSEC2.CustomIngressPorts),
		"the override value is the later empty slice — locks the IsZero-driven rule")
}

func TestUnionConfig_NilSlice_NoOverride(t *testing.T) {
	t.Parallel()

	// Part 1 populates AWSEC2.CustomIngressPorts; part 2 leaves it nil.
	// Result must keep the populated slice — nil src is zero and therefore
	// does not override.
	parts := []Config{
		{
			AWSEC2: &struct {
				InstanceType          string `json:"instanceType,omitempty"`
				NumServers            string `json:"numServers,omitempty"`
				NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
				DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
				UserData              string `json:"userData,omitempty"`
				UserDataURL           string `json:"userDataURL,omitempty"`
				CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
				SSHPublicKey          string `json:"sshPublicKey,omitempty"`
				EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
				GPUEnabled            *bool  `json:"gpuEnabled,omitempty"`
			}{
				CustomIngressPorts: []int{22, 80, 443},
			},
		},
		{
			AWSEC2: &struct {
				InstanceType          string `json:"instanceType,omitempty"`
				NumServers            string `json:"numServers,omitempty"`
				NumCoresPerServer     string `json:"numCoresPerServer,omitempty"`
				DiskSizePerServer     string `json:"diskSizePerServer,omitempty"`
				UserData              string `json:"userData,omitempty"`
				UserDataURL           string `json:"userDataURL,omitempty"`
				CustomIngressPorts    []int  `json:"customIngressPorts,omitempty"`
				SSHPublicKey          string `json:"sshPublicKey,omitempty"`
				EnableInstanceConnect *bool  `json:"enableInstanceConnect,omitempty"`
				GPUEnabled            *bool  `json:"gpuEnabled,omitempty"`
			}{
				InstanceType: "t3.large", // populated leaf
				// CustomIngressPorts left nil — must not override
			},
		},
	}
	got := UnionConfig(parts)

	require.NotNil(t, got.AWSEC2)
	assert.Equal(t, []int{22, 80, 443}, got.AWSEC2.CustomIngressPorts,
		"nil slice in a later part must not override an earlier populated slice")
	assert.Equal(t, "t3.large", got.AWSEC2.InstanceType,
		"later non-zero scalar must still override")
}

func TestUnionConfig_Idempotent(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Cloud:  "AWS",
		Region: "us-east-1",
	}
	cfg.AWSVPC = &struct {
		SingleNATGateway *bool `json:"singleNatGateway,omitempty"`
		EnableNATGateway *bool `json:"enableNatGateway,omitempty"`
		AZCount          *int  `json:"azCount,omitempty"`
	}{EnableNATGateway: boolPtr(true), AZCount: intPtr(2)}

	// Fold of one element must equal cfg (JSON-roundtrip equal — pointer
	// identity at the *struct level differs by design, that's fine).
	one := UnionConfig([]Config{cfg})
	assertConfigJSONEqual(t, cfg, one)

	// Fold of two copies must match fold of one — idempotent in the
	// "applying the same record twice changes nothing" sense.
	two := UnionConfig([]Config{cfg, cfg})
	assertConfigJSONEqual(t, one, two)
}

func TestUnionConfig_EmptyInput(t *testing.T) {
	t.Parallel()

	assert.Equal(t, Config{}, UnionConfig(nil), "nil input must yield zero Config")
	assert.Equal(t, Config{}, UnionConfig([]Config{}), "empty slice input must yield zero Config")
}

func TestUnionConfig_AWSBackupsRecursion(t *testing.T) {
	t.Parallel()

	// Part 1 sets AWSBackups.RDS.FrequencyHours; part 2 sets only
	// AWSBackups.S3.FrequencyHours. Result must have BOTH sub-sub-blocks
	// populated — proves recursion descends through the AWSBackups *struct
	// AND through the per-component *struct underneath.
	p1 := Config{}
	p1.AWSBackups = newAWSBackups()
	p1.AWSBackups.RDS = &struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	}{FrequencyHours: 4}

	p2 := Config{}
	p2.AWSBackups = newAWSBackups()
	p2.AWSBackups.S3 = &struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	}{FrequencyHours: 24}

	got := UnionConfig([]Config{p1, p2})

	require.NotNil(t, got.AWSBackups)
	require.NotNil(t, got.AWSBackups.RDS, "earlier-part backup leaf must survive")
	assert.Equal(t, 4, got.AWSBackups.RDS.FrequencyHours)
	require.NotNil(t, got.AWSBackups.S3, "later-part backup leaf must land")
	assert.Equal(t, 24, got.AWSBackups.S3.FrequencyHours)
}

// ──────────────────────────────────────────────────────────────────────────
// UnionComponents
// ──────────────────────────────────────────────────────────────────────────

func TestUnionComponents_LastNonZeroWins(t *testing.T) {
	t.Parallel()

	parts := []Components{
		{Cloud: "AWS", AWSVPC: "Public VPC"},
		{AWSVPC: "Private VPC"},
	}
	got := UnionComponents(parts)

	assert.Equal(t, "AWS", got.Cloud, "earlier non-zero scalar must survive when later part is zero")
	assert.Equal(t, "Private VPC", got.AWSVPC, "later non-zero string must override earlier")
}

func TestUnionComponents_PointerToFalsePreserved(t *testing.T) {
	t.Parallel()

	// Canonical #1043 case: explicit deselection must beat an earlier
	// explicit selection. Locks the pointer-non-nil discriminator.
	parts := []Components{
		{AWSOpenSearch: boolPtr(true)},
		{AWSOpenSearch: boolPtr(false)},
	}
	got := UnionComponents(parts)

	require.NotNil(t, got.AWSOpenSearch,
		"explicit *bool(&false) is non-nil and must override earlier *bool(&true)")
	assert.False(t, *got.AWSOpenSearch,
		"final dereferenced value must be the later part's false")
}

func TestUnionComponents_Recurses_AWSBackups(t *testing.T) {
	t.Parallel()

	// Part 1 selects RDS backups; part 2 selects S3 backups. Result must
	// have BOTH leaves populated under a single AWSBackups sub-struct —
	// proves recursion at the *struct level.
	p1 := Components{}
	p1.AWSBackups = &struct {
		EC2         *bool `json:"aws_ec2,omitempty"`
		RDS         *bool `json:"aws_rds,omitempty"`
		ElastiCache *bool `json:"aws_elasticache,omitempty"`
		DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
		S3          *bool `json:"aws_s3,omitempty"`
	}{RDS: boolPtr(true)}

	p2 := Components{}
	p2.AWSBackups = &struct {
		EC2         *bool `json:"aws_ec2,omitempty"`
		RDS         *bool `json:"aws_rds,omitempty"`
		ElastiCache *bool `json:"aws_elasticache,omitempty"`
		DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
		S3          *bool `json:"aws_s3,omitempty"`
	}{S3: boolPtr(true)}

	got := UnionComponents([]Components{p1, p2})

	require.NotNil(t, got.AWSBackups)
	require.NotNil(t, got.AWSBackups.RDS, "earlier part's RDS selection must survive")
	assert.True(t, *got.AWSBackups.RDS)
	require.NotNil(t, got.AWSBackups.S3, "later part's S3 selection must land")
	assert.True(t, *got.AWSBackups.S3)
}

func TestUnionComponents_CrossCloud_LastCloudWins(t *testing.T) {
	t.Parallel()

	// Part 1 is an AWS stack; part 2 is a GCP stack. Result has Cloud=GCP
	// and BOTH clouds' field sets survive — Union does merge only, NOT
	// normalize. Stripping the opposite-cloud fields is Components.Normalize's
	// job and must NOT happen here.
	parts := []Components{
		{Cloud: "AWS", AWSVPC: "Public VPC", AWSEC2: "Intel"},
		{Cloud: "GCP", GCPVPC: boolPtr(true), GCPCompute: "n2-standard-2"},
	}
	got := UnionComponents(parts)

	assert.Equal(t, "GCP", got.Cloud, "later Cloud must override")
	assert.Equal(t, "Public VPC", got.AWSVPC,
		"AWS field from earlier part must survive — Union does not auto-strip")
	assert.Equal(t, "Intel", got.AWSEC2,
		"AWS field from earlier part must survive — Union does not auto-strip")
	require.NotNil(t, got.GCPVPC, "GCP field from later part must land")
	assert.True(t, *got.GCPVPC)
	assert.Equal(t, "n2-standard-2", got.GCPCompute)
}

// TestUnion_Then_Normalize_PipelineStripsOppositeCloud locks the documented
// pipeline shape: UnionComponents intentionally keeps both clouds' fields,
// and Components.Normalize (the next pipeline step) is what strips the
// opposite-cloud residual. End-to-end test of the cross-file contract so a
// future refactor that conflates the two responsibilities can't slip past.
// /qa-professor P3-11.
func TestUnion_Then_Normalize_PipelineStripsOppositeCloud(t *testing.T) {
	t.Parallel()

	parts := []Components{
		{Cloud: "AWS", AWSVPC: "Public VPC", AWSEC2: "Intel", AWSLambda: boolPtr(true)},
		{Cloud: "GCP", GCPVPC: boolPtr(true), GCPCompute: "n2-standard-2"},
	}
	got := UnionComponents(parts)

	// Pre-Normalize: AWS residue survives (this is Union's intent).
	require.Equal(t, "GCP", got.Cloud)
	require.Equal(t, "Public VPC", got.AWSVPC,
		"pre-Normalize: AWS field from earlier part survives the Union — Union does not strip")
	require.Equal(t, "Intel", got.AWSEC2)

	// Pipeline step: Normalize.
	got.Normalize()

	// Post-Normalize: AWS residue is gone, GCP survives.
	assert.Equal(t, "", got.AWSVPC,
		"post-Normalize: AWS VPC field must be cleared on a Cloud=GCP value")
	assert.Equal(t, "", got.AWSEC2,
		"post-Normalize: AWS EC2 field must be cleared on a Cloud=GCP value")
	assert.Nil(t, got.AWSLambda,
		"post-Normalize: AWS Lambda pointer must be cleared on a Cloud=GCP value")
	require.NotNil(t, got.GCPVPC, "post-Normalize: GCP fields survive")
	assert.True(t, *got.GCPVPC)
	assert.Equal(t, "n2-standard-2", got.GCPCompute)
}

// ──────────────────────────────────────────────────────────────────────────
// Composition with the PR-1 coherence pipeline
// ──────────────────────────────────────────────────────────────────────────

func TestUnion_Then_StripOrphanConfig_PipelineSequence(t *testing.T) {
	t.Parallel()

	// Two-part fold then strip — verifies UnionConfig output flows cleanly
	// through StripOrphanConfig (PR-1). Part 1 has OpenSearch selected with
	// config; part 2 explicitly deselects it. After UnionConfig the *bool
	// pointer is &false but the cfg.AWSOpenSearch sub-block survives from
	// part 1 — StripOrphanConfig must then clear it.
	cfgPart1 := Config{
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{DeploymentType: "Production", InstanceType: "m6g.large"},
	}
	cfgPart2 := Config{} // empty — does not touch AWSOpenSearch

	mergedCfg := UnionConfig([]Config{cfgPart1, cfgPart2})
	require.NotNil(t, mergedCfg.AWSOpenSearch,
		"merged Config retains AWSOpenSearch sub-block from part 1")

	// Components fold: part 1 selects OpenSearch; part 2 deselects it.
	mergedComps := UnionComponents([]Components{
		{AWSOpenSearch: boolPtr(true)},
		{AWSOpenSearch: boolPtr(false)},
	})
	require.NotNil(t, mergedComps.AWSOpenSearch)
	assert.False(t, *mergedComps.AWSOpenSearch,
		"explicit-deselect must win the Components fold")

	// Now run the PR-1 strip — the orphan cfg.AWSOpenSearch must go.
	StripOrphanConfig(&mergedComps, &mergedCfg)
	assert.Nil(t, mergedCfg.AWSOpenSearch,
		"StripOrphanConfig must clear cfg.AWSOpenSearch when the component is explicitly deselected")
}

// ──────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────

// newAWSBackups returns a zero-valued AWSBackups sub-struct pointer in the
// exact anonymous shape declared on Config. Helper because the literal type
// is verbose to write inline.
func newAWSBackups() *struct {
	EC2 *struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	} `json:"aws_ec2,omitempty"`
	RDS *struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	} `json:"aws_rds,omitempty"`
	ElastiCache *struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	} `json:"aws_elasticache,omitempty"`
	DynamoDB *struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	} `json:"aws_dynamodb,omitempty"`
	S3 *struct {
		FrequencyHours int    `json:"frequencyHours,omitempty"`
		RetentionDays  int    `json:"retentionDays,omitempty"`
		Region         string `json:"region,omitempty"`
	} `json:"aws_s3,omitempty"`
} {
	return &struct {
		EC2 *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_ec2,omitempty"`
		RDS *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_rds,omitempty"`
		ElastiCache *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_elasticache,omitempty"`
		DynamoDB *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_dynamodb,omitempty"`
		S3 *struct {
			FrequencyHours int    `json:"frequencyHours,omitempty"`
			RetentionDays  int    `json:"retentionDays,omitempty"`
			Region         string `json:"region,omitempty"`
		} `json:"aws_s3,omitempty"`
	}{}
}

// assertConfigJSONEqual checks two Configs are equivalent under JSON
// marshal-roundtrip — the equality that matters for persistence parity.
// Pointer identity at the *struct level may differ; that's expected and fine.
func assertConfigJSONEqual(t *testing.T, want, got Config) {
	t.Helper()
	wb, err := json.Marshal(want)
	require.NoError(t, err)
	gb, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(wb), string(gb))
}
