package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// samplePolicyDocument is a minimal valid IAM policy document used
// across the tests below.
const samplePolicyDocument = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`

// fakeIAMPolicyAPI is a hand-rolled iamPolicyAPI fake: each method
// returns the canned output/error stored on the struct and records the
// argument it was called with so tests can assert call wiring.
type fakeIAMPolicyAPI struct {
	getPolicyOut *iam.GetPolicyOutput
	getPolicyErr error

	getVersionOut *iam.GetPolicyVersionOutput
	getVersionErr error

	gotPolicyARN     string // PolicyArn passed to GetPolicy
	gotVersionARN    string // PolicyArn passed to GetPolicyVersion
	gotVersionID     string // VersionId passed to GetPolicyVersion
	getVersionCalled bool
}

func (f *fakeIAMPolicyAPI) GetPolicy(_ context.Context, in *iam.GetPolicyInput, _ ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	f.gotPolicyARN = aws.ToString(in.PolicyArn)
	return f.getPolicyOut, f.getPolicyErr
}

func (f *fakeIAMPolicyAPI) GetPolicyVersion(_ context.Context, in *iam.GetPolicyVersionInput, _ ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error) {
	f.getVersionCalled = true
	f.gotVersionARN = aws.ToString(in.PolicyArn)
	f.gotVersionID = aws.ToString(in.VersionId)
	return f.getVersionOut, f.getVersionErr
}

var _ iamPolicyAPI = (*fakeIAMPolicyAPI)(nil)

func TestIAMPolicyEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_iam_policy", newIAMPolicyEnricher().ResourceType())
}

func TestIAMPolicyEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := iamPolicyEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"arn": "arn:aws:iam::123456789012:policy/MyPolicy"},
		},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMPolicyEnricher_CannotResolveARN(t *testing.T) {
	t.Parallel()
	enr := iamPolicyEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{},
	}, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive policy ARN")
}

func TestIAMPolicyEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	const lookupARN = "arn:aws:iam::123456789012:policy/MyPolicy"
	// The API-returned ARN differs from the lookup ARN so the test can
	// prove the stamp prefers the API value.
	const apiARN = "arn:aws:iam::123456789012:policy/some/path/MyPolicy"
	enr := iamPolicyEnricher{fetch: func(_ context.Context, _ *iam.Client, gotARN string) (*iamPolicyData, error) {
		assert.Equal(t, lookupARN, gotARN)
		return &iamPolicyData{
			Policy: &iamtypes.Policy{
				Arn:         aws.String(apiARN),
				PolicyName:  aws.String("MyPolicy"),
				Path:        aws.String("/some/path/"),
				Description: aws.String("my managed policy"),
				Tags: []iamtypes.Tag{
					{Key: aws.String("Project"), Value: aws.String("io-abc")},
				},
			},
			Document: samplePolicyDocument,
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		ImportID: lookupARN,
	}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{IAM: &iam.Client{}}))

	// The API-returned ARN wins over the lookup ARN when stamping.
	assert.Equal(t, apiARN, ir.Identity.NativeIDs["arn"])

	var got generated.AWSIAMPolicy
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.Name)
	assert.Equal(t, "MyPolicy", *got.Name.Literal)
	require.NotNil(t, got.Path)
	assert.Equal(t, "/some/path/", *got.Path.Literal)
	require.NotNil(t, got.Description)
	assert.Equal(t, "my managed policy", *got.Description.Literal)
	require.NotNil(t, got.Policy)
	assert.JSONEq(t, samplePolicyDocument, *got.Policy.Literal)
	require.NotNil(t, got.Tags["Project"])
	assert.Equal(t, "io-abc", *got.Tags["Project"].Literal)

	// Computed-only fields are NOT populated (decision #5).
	assert.Nil(t, got.ARN)
	assert.Nil(t, got.AttachmentCount)
	assert.Nil(t, got.PolicyID)
	assert.Nil(t, got.ID)
}

func TestIAMPolicyEnricher_StampFallsBackToLookupARN(t *testing.T) {
	t.Parallel()
	const lookupARN = "arn:aws:iam::123456789012:policy/MyPolicy"
	// Policy.Arn is nil — the stamp must fall back to the lookup ARN.
	enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
		return &iamPolicyData{
			Policy:   &iamtypes.Policy{PolicyName: aws.String("MyPolicy")},
			Document: samplePolicyDocument,
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: lookupARN}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{IAM: &iam.Client{}}))
	assert.Equal(t, lookupARN, ir.Identity.NativeIDs["arn"])
}

func TestIAMPolicyEnricher_NoSuchEntityMapsToNotFound(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"NoSuchEntity", "NoSuchEntityException"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
				return nil, &fakeIAMSmithyErr{code: code}
			}}
			err := enr.Enrich(context.Background(), &imported.ImportedResource{
				Identity: imported.ResourceIdentity{ImportID: "arn:aws:iam::123456789012:policy/Gone"},
			}, EnrichClients{IAM: &iam.Client{}})
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestIAMPolicyEnricher_AccessDeniedIsNotNotFound(t *testing.T) {
	t.Parallel()
	// A non-NoSuchEntity smithy error must NOT be downgraded to
	// ErrNotFound — it is a real failure the batch should surface.
	enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
		return nil, &fakeIAMSmithyErr{code: "AccessDenied"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{ImportID: "arn:aws:iam::123456789012:policy/Denied"},
	}, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	// The ARN is wrapped into the error context.
	assert.Contains(t, err.Error(), "arn:aws:iam::123456789012:policy/Denied")
}

func TestIAMPolicyEnricher_UnexpectedErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
		return nil, want
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{ImportID: "arn:aws:iam::123456789012:policy/X"},
	}, EnrichClients{IAM: &iam.Client{}})
	require.ErrorIs(t, err, want)
	assert.Contains(t, err.Error(), "arn:aws:iam::123456789012:policy/X")
}

func TestIAMPolicyEnricher_EmptyResponseIsError(t *testing.T) {
	t.Parallel()
	// fetch returning (nil, nil) must surface as an explicit error,
	// not a silently-empty Attrs payload.
	enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
		return nil, nil
	}}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{ImportID: "arn:aws:iam::123456789012:policy/X"},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")
	assert.Nil(t, ir.Attrs)
}

func TestIAMPolicyEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:iam::123456789012:policy/MyPolicy"
	enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
		return &iamPolicyData{
			Policy:   &iamtypes.Policy{Arn: aws.String(arn), PolicyName: aws.String("MyPolicy")},
			Document: samplePolicyDocument,
		}, nil
	}}
	identity := &imported.ResourceIdentity{NativeIDs: map[string]string{"arn": arn}}
	raw, err := enr.EnrichByID(context.Background(), identity, EnrichClients{IAM: &iam.Client{}})
	require.NoError(t, err)

	// EnrichByID must not mutate identity — unlike Enrich, it does not
	// stamp the ARN or any other field. NativeIDs is left exactly as
	// the caller passed it.
	assert.Equal(t, map[string]string{"arn": arn}, identity.NativeIDs)
	assert.Empty(t, identity.Address)

	var got generated.AWSIAMPolicy
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.Policy)
	assert.JSONEq(t, samplePolicyDocument, *got.Policy.Literal)
}

func TestIAMPolicyEnricher_EnrichByID_NilClient(t *testing.T) {
	t.Parallel()
	enr := iamPolicyEnricher{}
	_, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{NativeIDs: map[string]string{"arn": "arn:x"}}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMPolicyEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := iamPolicyEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestIAMPolicyEnricher_EnrichByID_NotFound(t *testing.T) {
	t.Parallel()
	enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
		return nil, &fakeIAMSmithyErr{code: "NoSuchEntityException"}
	}}
	_, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "arn:aws:iam::123456789012:policy/Gone"},
		EnrichClients{IAM: &iam.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIAMPolicyEnricher_EnrichByID_EmptyResponseIsError(t *testing.T) {
	t.Parallel()
	enr := iamPolicyEnricher{fetch: func(context.Context, *iam.Client, string) (*iamPolicyData, error) {
		return nil, nil
	}}
	_, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "arn:aws:iam::123456789012:policy/X"},
		EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")
}

func TestIAMPolicyARNForEnrich(t *testing.T) {
	t.Parallel()
	t.Run("NativeIDs wins", func(t *testing.T) {
		t.Parallel()
		got := iamPolicyARNForEnrich(&imported.ResourceIdentity{
			NativeIDs: map[string]string{"arn": "arn:native"},
			ImportID:  "arn:import",
		})
		assert.Equal(t, "arn:native", got)
	})
	t.Run("ImportID fallback", func(t *testing.T) {
		t.Parallel()
		got := iamPolicyARNForEnrich(&imported.ResourceIdentity{ImportID: "arn:import"})
		assert.Equal(t, "arn:import", got)
	})
	t.Run("nil identity", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", iamPolicyARNForEnrich(nil))
	})
	t.Run("NameHint is not a fallback", func(t *testing.T) {
		t.Parallel()
		// GetPolicy requires a full ARN; the bare policy name in
		// NameHint must not be used.
		got := iamPolicyARNForEnrich(&imported.ResourceIdentity{NameHint: "MyPolicy"})
		assert.Equal(t, "", got)
	})
	t.Run("whitespace is trimmed", func(t *testing.T) {
		t.Parallel()
		got := iamPolicyARNForEnrich(&imported.ResourceIdentity{NativeIDs: map[string]string{"arn": "  arn:x  "}})
		assert.Equal(t, "arn:x", got)
	})
}

func TestDecodeIAMPolicyDocument(t *testing.T) {
	t.Parallel()
	t.Run("URL-encoded document is decoded", func(t *testing.T) {
		t.Parallel()
		// The IAM API returns the document URL-encoded (RFC 3986).
		encoded := url.QueryEscape(samplePolicyDocument)
		got, err := decodeIAMPolicyDocument(encoded)
		require.NoError(t, err)
		assert.JSONEq(t, samplePolicyDocument, got)
	})
	t.Run("already-decoded document is compacted", func(t *testing.T) {
		t.Parallel()
		pretty := "{\n  \"Version\": \"2012-10-17\"\n}"
		got, err := decodeIAMPolicyDocument(pretty)
		require.NoError(t, err)
		assert.Equal(t, `{"Version":"2012-10-17"}`, got)
	})
	t.Run("malformed escape falls back to raw", func(t *testing.T) {
		t.Parallel()
		// A bare "%" is not a valid escape; url.QueryUnescape errors,
		// the raw value is used as-is and then compacted.
		got, err := decodeIAMPolicyDocument(`{"k": "100% done"}`)
		require.NoError(t, err)
		assert.Equal(t, `{"k":"100% done"}`, got)
	})
	t.Run("empty document yields empty string", func(t *testing.T) {
		t.Parallel()
		got, err := decodeIAMPolicyDocument("")
		require.NoError(t, err)
		assert.Equal(t, "", got)
	})
	t.Run("non-JSON document is an error", func(t *testing.T) {
		t.Parallel()
		_, err := decodeIAMPolicyDocument("not json")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not valid JSON")
	})
}

func TestMapIAMPolicy(t *testing.T) {
	t.Parallel()
	t.Run("nil input yields empty struct", func(t *testing.T) {
		t.Parallel()
		got := mapIAMPolicy(nil)
		require.NotNil(t, got)
		assert.Nil(t, got.Name)
		assert.Nil(t, got.Policy)
	})
	t.Run("empty document omits policy field", func(t *testing.T) {
		t.Parallel()
		got := mapIAMPolicy(&iamPolicyData{
			Policy:   &iamtypes.Policy{PolicyName: aws.String("P")},
			Document: "",
		})
		assert.Nil(t, got.Policy, "empty document must leave policy unset")
		require.NotNil(t, got.Name)
	})
	t.Run("empty-string metadata fields are omitted", func(t *testing.T) {
		t.Parallel()
		got := mapIAMPolicy(&iamPolicyData{
			Policy: &iamtypes.Policy{
				PolicyName:  aws.String(""),
				Path:        aws.String(""),
				Description: aws.String(""),
			},
			Document: samplePolicyDocument,
		})
		assert.Nil(t, got.Name)
		assert.Nil(t, got.Path)
		assert.Nil(t, got.Description)
	})
	t.Run("nil-key tag is skipped without panic", func(t *testing.T) {
		t.Parallel()
		got := mapIAMPolicy(&iamPolicyData{
			Policy: &iamtypes.Policy{
				Tags: []iamtypes.Tag{
					{Key: nil, Value: aws.String("orphan")},
					{Key: aws.String("Project"), Value: aws.String("io-abc")},
				},
			},
		})
		require.Len(t, got.Tags, 1)
		require.NotNil(t, got.Tags["Project"])
		assert.Equal(t, "io-abc", *got.Tags["Project"].Literal)
	})
	t.Run("all-nil-key tags yield nil map", func(t *testing.T) {
		t.Parallel()
		got := mapIAMPolicy(&iamPolicyData{
			Policy: &iamtypes.Policy{
				Tags: []iamtypes.Tag{{Key: nil, Value: aws.String("orphan")}},
			},
		})
		assert.Nil(t, got.Tags, "a tag map with no usable keys must stay nil, not {}")
	})
}

func TestFetchIAMPolicyWithClient(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:iam::123456789012:policy/MyPolicy"

	t.Run("happy path wires DefaultVersionId into GetPolicyVersion", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMPolicyAPI{
			getPolicyOut: &iam.GetPolicyOutput{Policy: &iamtypes.Policy{
				Arn:              aws.String(arn),
				PolicyName:       aws.String("MyPolicy"),
				DefaultVersionId: aws.String("v3"),
			}},
			getVersionOut: &iam.GetPolicyVersionOutput{PolicyVersion: &iamtypes.PolicyVersion{
				Document: aws.String(url.QueryEscape(samplePolicyDocument)),
			}},
		}
		data, err := fetchIAMPolicyWithClient(context.Background(), f, arn)
		require.NoError(t, err)
		require.NotNil(t, data)
		// GetPolicy and GetPolicyVersion both received the ARN.
		assert.Equal(t, arn, f.gotPolicyARN)
		assert.Equal(t, arn, f.gotVersionARN)
		// GetPolicyVersion was called with the version id GetPolicy
		// reported as the default — not a hard-coded value.
		assert.Equal(t, "v3", f.gotVersionID)
		assert.JSONEq(t, samplePolicyDocument, data.Document)
	})

	t.Run("GetPolicy error is wrapped", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMPolicyAPI{getPolicyErr: errors.New("boom")}
		_, err := fetchIAMPolicyWithClient(context.Background(), f, arn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "iam:GetPolicy")
		assert.False(t, f.getVersionCalled, "GetPolicyVersion must not run after GetPolicy fails")
	})

	t.Run("nil Policy in GetPolicy response is an error", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMPolicyAPI{getPolicyOut: &iam.GetPolicyOutput{Policy: nil}}
		_, err := fetchIAMPolicyWithClient(context.Background(), f, arn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil policy")
		assert.False(t, f.getVersionCalled)
	})

	t.Run("missing DefaultVersionId is an error", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMPolicyAPI{getPolicyOut: &iam.GetPolicyOutput{Policy: &iamtypes.Policy{
			Arn: aws.String(arn),
		}}}
		_, err := fetchIAMPolicyWithClient(context.Background(), f, arn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DefaultVersionId")
		assert.False(t, f.getVersionCalled)
	})

	t.Run("GetPolicyVersion error is wrapped", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMPolicyAPI{
			getPolicyOut: &iam.GetPolicyOutput{Policy: &iamtypes.Policy{
				DefaultVersionId: aws.String("v1"),
			}},
			getVersionErr: errors.New("boom"),
		}
		_, err := fetchIAMPolicyWithClient(context.Background(), f, arn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "iam:GetPolicyVersion")
	})

	t.Run("nil PolicyVersion in response is an error", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMPolicyAPI{
			getPolicyOut: &iam.GetPolicyOutput{Policy: &iamtypes.Policy{
				DefaultVersionId: aws.String("v1"),
			}},
			getVersionOut: &iam.GetPolicyVersionOutput{PolicyVersion: nil},
		}
		_, err := fetchIAMPolicyWithClient(context.Background(), f, arn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil policy version")
	})

	t.Run("NoSuchEntity from GetPolicy surfaces and maps to NotFound", func(t *testing.T) {
		t.Parallel()
		// fetchIAMPolicyWithClient wraps the smithy error; the enricher
		// then maps it to ErrNotFound via isAPIErrorCode (errors.As
		// unwraps through the fmt.Errorf %w chain).
		f := &fakeIAMPolicyAPI{getPolicyErr: &fakeIAMSmithyErr{code: "NoSuchEntity"}}
		_, err := fetchIAMPolicyWithClient(context.Background(), f, arn)
		require.Error(t, err)
		assert.True(t, isAPIErrorCode(err, "NoSuchEntity"),
			"wrapped smithy error must remain inspectable through the wrap chain")
	})
}

func TestDefaultIAMPolicyFetch_NilClient(t *testing.T) {
	t.Parallel()
	_, err := defaultIAMPolicyFetch(context.Background(), nil, "arn:x")
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMPolicyEnricher_RegisteredAsOverride(t *testing.T) {
	t.Parallel()
	// The hand-rolled enricher must win over the generic Cloud Control
	// fallback for aws_iam_policy (#661).
	d := NewAWSDiscoverer(aws.Config{Region: "us-east-1"})
	enr, ok := d.byTypeEnricher["aws_iam_policy"]
	require.True(t, ok, "aws_iam_policy must have a registered enricher")
	_, isHandRolled := enr.(*iamPolicyEnricher)
	assert.True(t, isHandRolled, "aws_iam_policy enricher must be the hand-rolled override, got %T", enr)
}
