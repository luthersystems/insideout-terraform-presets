package planredact

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Known secret values planted in the fixture plan. Each one sits where
// `terraform show -json` really writes it: the input variables (the cloud
// credentials the wrapper passes as tfvars, sandbox-infrastructure-template#160
// amendment 5), a sensitive variable's default, and sensitive attributes and
// outputs in planned_values, prior_state, resource_changes, resource_drift,
// deferred_changes and output_changes. If any of them survives redaction, the
// plan JSON still leaks a credential (luthersystems/reliable#2553).
const (
	planLeakAWSKey      = "AKIAPLANSECRET0000001"
	planLeakAWSSAK      = "planLeakAccessKey/0123456789abcdefghijABCD"
	planLeakGCPAccount  = "eyJ0eXBlIjoic2VydmljZV9hY2NvdW50IiwicHJpdmF0ZV9rZXkiOiJQTEFOU0VDUkVUIn0="
	planLeakExternalID  = "plan-external-id-secret-42"
	planLeakDefault     = "default-db-password-secret"
	planLeakDBAttr      = "planned-db-password-secret"
	planLeakPriorDB     = "prior-db-password-secret"
	planLeakDriftDB     = "drift-db-password-secret"
	planLeakDeferred    = "deferred-token-secret"
	planLeakOutput      = "output-connection-string-secret"
	planLeakChildModule = "child-module-api-key-secret"
	planLeakListElem    = "list-element-secret"
	planLeakProviderExt = "provider-external-id-secret-77"
	planLeakModuleInput = "module-input-literal-secret"
	planLeakPlainDef    = "plain-default-external-id-secret"
	planLeakCfgOutput   = "config-output-literal-secret"
	planLeakCfgResource = "config-resource-literal-secret"
)

var planLeaks = []string{
	planLeakAWSKey, planLeakAWSSAK, planLeakGCPAccount, planLeakExternalID,
	planLeakDefault, planLeakDBAttr, planLeakPriorDB, planLeakDriftDB,
	planLeakDeferred, planLeakOutput, planLeakChildModule, planLeakListElem,
	planLeakProviderExt, planLeakModuleInput, planLeakPlainDef,
	planLeakCfgOutput, planLeakCfgResource,
}

// planWithSecrets is a trimmed `terraform show -json` plan that carries every
// secret above, plus non-sensitive values the redaction must keep (the region,
// the bucket name, the change actions, an "importing" block, a non-sensitive
// output) so the plan gate and the operator views still work on it.
const planWithSecrets = `{
  "format_version": "1.2",
  "terraform_version": "1.9.8",
  "variables": {
    "region":               {"value": "us-west-2"},
    "aws_access_key_id":    {"value": "` + planLeakAWSKey + `"},
    "aws_secret_access_key":{"value": "` + planLeakAWSSAK + `"},
    "gcp_credentials_b64":  {"value": "` + planLeakGCPAccount + `"},
    "aws_external_id":      {"value": "` + planLeakExternalID + `"},
    "tags":                 {"value": {"Owner": "` + planLeakExternalID + `"}}
  },
  "planned_values": {
    "outputs": {
      "bucket_name": {"sensitive": false, "value": "kept-bucket"},
      "conn":        {"sensitive": true,  "value": "` + planLeakOutput + `"}
    },
    "root_module": {
      "resources": [
        {
          "address": "aws_s3_bucket.assets",
          "type": "aws_s3_bucket",
          "values": {"bucket": "kept-bucket", "tags": {"Name": "kept-bucket"}},
          "sensitive_values": {"tags": {}}
        },
        {
          "address": "aws_db_instance.main",
          "type": "aws_db_instance",
          "values": {
            "identifier": "kept-db",
            "password": "` + planLeakDBAttr + `",
            "hosts": ["kept-host", "` + planLeakListElem + `"]
          },
          "sensitive_values": {"password": true, "hosts": [false, true]}
        }
      ],
      "child_modules": [
        {
          "address": "module.api",
          "resources": [
            {
              "address": "module.api.aws_ssm_parameter.key",
              "values": {"name": "kept-param", "value": "` + planLeakChildModule + `"},
              "sensitive_values": {"value": true}
            }
          ]
        }
      ]
    }
  },
  "prior_state": {
    "format_version": "1.0",
    "values": {
      "root_module": {
        "resources": [
          {
            "address": "aws_db_instance.main",
            "values": {"identifier": "kept-db", "password": "` + planLeakPriorDB + `"},
            "sensitive_values": {"password": true}
          }
        ]
      }
    }
  },
  "resource_changes": [
    {
      "address": "aws_s3_bucket.assets",
      "mode": "managed",
      "type": "aws_s3_bucket",
      "change": {
        "actions": ["no-op"],
        "before": {"bucket": "kept-bucket"},
        "after": {"bucket": "kept-bucket"},
        "before_sensitive": {},
        "after_sensitive": {},
        "importing": {"id": "kept-bucket"}
      }
    },
    {
      "address": "aws_db_instance.main",
      "mode": "managed",
      "type": "aws_db_instance",
      "change": {
        "actions": ["update"],
        "before": {"identifier": "kept-db", "password": "` + planLeakPriorDB + `"},
        "after": {"identifier": "kept-db", "password": "` + planLeakDBAttr + `"},
        "after_unknown": {},
        "before_sensitive": {"password": true},
        "after_sensitive": {"password": true}
      }
    }
  ],
  "resource_drift": [
    {
      "address": "aws_db_instance.main",
      "change": {
        "actions": ["update"],
        "before": {"password": "` + planLeakPriorDB + `"},
        "after": {"password": "` + planLeakDriftDB + `"},
        "before_sensitive": {"password": true},
        "after_sensitive": {"password": true}
      }
    }
  ],
  "deferred_changes": [
    {
      "reason": "provider_config_unknown",
      "resource_change": {
        "address": "aws_ssm_parameter.later",
        "change": {
          "actions": ["create"],
          "before": null,
          "after": {"value": "` + planLeakDeferred + `"},
          "after_sensitive": {"value": true}
        }
      }
    }
  ],
  "output_changes": {
    "bucket_name": {"actions": ["create"], "before": null, "after": "kept-bucket", "before_sensitive": false, "after_sensitive": false},
    "conn":        {"actions": ["create"], "before": null, "after": "` + planLeakOutput + `", "before_sensitive": false, "after_sensitive": true}
  },
  "configuration": {
    "provider_config": {
      "aws.imported": {
        "name": "aws",
        "alias": "imported",
        "expressions": {
          "region": {"constant_value": "us-west-2"},
          "assume_role": [
            {
              "role_arn": {"references": ["var.bootstrap_role"]},
              "external_id": {"constant_value": "` + planLeakProviderExt + `"}
            }
          ]
        }
      }
    },
    "root_module": {
      "variables": {
        "region":      {"default": "us-west-2"},
        "db_password": {"default": "` + planLeakDefault + `", "sensitive": true},
        "aws_external_id": {"default": "` + planLeakPlainDef + `"}
      },
      "outputs": {
        "conn": {"sensitive": true, "expression": {"constant_value": "` + planLeakCfgOutput + `"}}
      },
      "resources": [
        {
          "address": "aws_db_instance.main",
          "mode": "managed",
          "type": "aws_db_instance",
          "name": "main",
          "expressions": {"password": {"constant_value": "` + planLeakCfgResource + `"}}
        }
      ],
      "module_calls": {
        "api": {
          "source": "./api",
          "expressions": {"api_key": {"constant_value": "` + planLeakModuleInput + `"}},
          "module": {
            "variables": {
              "api_key": {"default": "` + planLeakChildModule + `", "sensitive": true}
            }
          }
        }
      }
    }
  }
}`

// jsonObj asserts v is a decoded JSON object.
func jsonObj(t *testing.T, v any) map[string]any {
	t.Helper()
	obj, ok := v.(map[string]any)
	require.True(t, ok, "want a JSON object, got %T", v)
	return obj
}

func assertNoPlanSecrets(t *testing.T, out []byte) {
	t.Helper()
	for _, s := range planLeaks {
		assert.NotContains(t, string(out), s, "plan JSON still carries a sensitive value")
	}
}

// TestRedactPlanJSON_RemovesKnownSecrets is the luthersystems/reliable#2553 proof: a plan
// with credential tfvars and sensitive attributes comes out without any of
// their values, and keeps the non-sensitive data the plan gate reads.
func TestRedactPlanJSON_RemovesKnownSecrets(t *testing.T) {
	t.Parallel()

	// The fixture must really carry each secret, or the test proves nothing.
	for _, s := range planLeaks {
		require.Contains(t, planWithSecrets, s)
	}

	out, err := JSON([]byte(planWithSecrets))
	require.NoError(t, err)
	require.True(t, json.Valid(out))
	assertNoPlanSecrets(t, out)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))

	// Every input variable value is gone, the names stay. Non-sensitive
	// variables are redacted too: Terraform does not mark a tfvar as
	// sensitive in .variables, and the credential tfvars are not always
	// declared sensitive.
	vars := jsonObj(t, got["variables"])
	for _, name := range []string{"region", "aws_access_key_id", "aws_secret_access_key", "gcp_credentials_b64", "aws_external_id", "tags"} {
		require.Contains(t, vars, name)
		assert.Equal(t, Placeholder, jsonObj(t, vars[name])["value"], name)
	}

	// Non-sensitive data stays exact.
	s := string(out)
	for _, kept := range []string{"kept-bucket", "kept-db", "kept-host", "kept-param", `"importing"`, `"no-op"`, "var.bootstrap_role"} {
		assert.Contains(t, s, kept)
	}
	planned := jsonObj(t, got["planned_values"])
	outputs := jsonObj(t, planned["outputs"])
	assert.Equal(t, "kept-bucket", jsonObj(t, outputs["bucket_name"])["value"])
	assert.Equal(t, Placeholder, jsonObj(t, outputs["conn"])["value"])

	// A partly sensitive list keeps its shape and its non-sensitive element.
	resources, ok := jsonObj(t, planned["root_module"])["resources"].([]any)
	require.True(t, ok)
	require.Len(t, resources, 2)
	db := jsonObj(t, resources[1])
	assert.Equal(t, []any{"kept-host", Placeholder}, jsonObj(t, db["values"])["hosts"])
}

// TestRedactPlanJSON_PreservesNumbers keeps a plan's numbers exact: a large
// integer must not come back as a rounded float.
func TestRedactPlanJSON_PreservesNumbers(t *testing.T) {
	t.Parallel()
	out, err := JSON([]byte(`{"format_version":"1.2","planned_values":{"root_module":{"resources":[{"values":{"port":9007199254740993}}]}}}`))
	require.NoError(t, err)
	assert.Contains(t, string(out), "9007199254740993")
}

// TestRedactPlanJSON_Invalid fails closed: bytes that are not a JSON object
// are an error, so a caller never serves them unredacted.
func TestRedactPlanJSON_Invalid(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"not json", `["a"]`, `"x"`, `{"variables":{"a":{"value":"x"}}} trailing`} {
		_, err := JSON([]byte(in))
		assert.Error(t, err, in)
	}
}

// TestJSON_State: `terraform show -json` of a state keeps its secrets
// under a top-level "values"; it is caught and redacted like a plan.
func TestJSON_State(t *testing.T) {
	t.Parallel()
	state := []byte(`{"format_version":"1.0","terraform_version":"1.9.8","values":{"outputs":{"db":{"sensitive":true,"value":"` + planLeakOutput + `"}},"root_module":{"resources":[{"address":"aws_db_instance.main","values":{"password":"` + planLeakDBAttr + `","name":"kept-db"},"sensitive_values":{"password":true}}]}}}`)
	require.True(t, LooksLikePlan(state))
	out, err := JSON(state)
	require.NoError(t, err)
	assert.NotContains(t, string(out), planLeakOutput)
	assert.NotContains(t, string(out), planLeakDBAttr)
	assert.Contains(t, string(out), "kept-db")
}

// TestLooksLikePlan: a plan is caught whole, renamed, or cut off; other
// JSON is not.
func TestLooksLikePlan(t *testing.T) {
	t.Parallel()
	assert.True(t, LooksLikePlan([]byte(planWithSecrets)))
	assert.True(t, LooksLikePlan([]byte(planWithSecrets[:len(planWithSecrets)/2])), "a truncated plan")
	assert.True(t, LooksLikePlan([]byte(planWithSecrets+"\n{}")), "a plan with trailing data")
	assert.False(t, LooksLikePlan([]byte(`[{"identity":{"type":"aws_s3_bucket"}}]`)))
	// Valid JSON that is not an object is never a plan, even when an
	// attribute names plan members (imported.json is an array).
	assert.False(t, LooksLikePlan([]byte(`[{"attrs":{"format_version":"1","values":[1]}}]`)))
	assert.False(t, LooksLikePlan([]byte(`{"format_version":"1.0","status":"succeeded"}`)))
	assert.False(t, LooksLikePlan([]byte("resource \"x\" \"y\" {}")))
}

// TestHCL: generated provider HCL loses the assume_role external ID; the
// rest of the file, references included, stays.
func TestHCL(t *testing.T) {
	t.Parallel()
	providers := []byte("provider \"aws\" {\n  alias  = \"imported\"\n  region = \"us-west-2\"\n  assume_role {\n    role_arn    = \"arn:aws:iam::123456789012:role/x\"\n    external_id = \"" + planLeakProviderExt + "\" # set by the job\n  }\n  token = var.token\n}\n")
	out := string(HCL(providers))
	assert.NotContains(t, out, planLeakProviderExt)
	assert.Contains(t, out, `external_id = "`+Placeholder+`" # set by the job`)
	assert.Contains(t, out, "arn:aws:iam::123456789012:role/x")
	assert.Contains(t, out, "token = var.token")

	// A one-line block and a web identity token are caught too.
	oneLine := []byte("provider \"aws\" { assume_role { external_id = \"" + planLeakProviderExt + "\" } }\n")
	assert.NotContains(t, string(HCL(oneLine)), planLeakProviderExt)
	wit := []byte("  assume_role_with_web_identity {\n    web_identity_token = \"" + planLeakDeferred + "\"\n  }\n")
	assert.NotContains(t, string(HCL(wit)), planLeakDeferred)
	// A name that only ends in a credential word is not a credential.
	assert.Contains(t, string(HCL([]byte("  my_token_name = \"kept\"\n"))), `"kept"`)

	plain := []byte("resource \"aws_s3_bucket\" \"assets\" {\n  bucket = \"kept\"\n}\n")
	assert.Equal(t, string(plain), string(HCL(plain)))
}
