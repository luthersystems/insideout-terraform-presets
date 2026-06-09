package reverseimport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	tfVarBootstrapRole = "bootstrap_role"
	tfVarAWSExternalID = "aws_external_id"
	tfEnvPrefix        = "TF_VAR_"
)

// AWSProviderAuth is the customer-account assume-role identity the
// reverse-import engine reaches the customer's AWS account with. Terraform's
// generated provider blocks assume RoleARN (with the optional ExternalID); the
// closure/dep-chase discoverer MUST assume the same role so its direct AWS SDK
// calls run as the same principal rather than the ambient pod/CLI credentials
// (#739). Exported so the discoverer-construction site (reversedisco / Mars /
// the CLI) and the engine share one resolution path.
type AWSProviderAuth struct {
	RoleARN    string
	ExternalID string
}

// ResolveAWSProviderAuth resolves the customer-account assume-role identity
// from (in priority order) the TF_VAR_bootstrap_role / TF_VAR_aws_external_id
// environment, the generated outputs/cloud-provision.json terraform_role
// output, and the tf/auto-vars bundle — the same sources Terraform's provider
// blocks use. outputDir is any path inside the generated stack; the project
// root is discovered by walking upward. A zero-value result (empty RoleARN) is
// not an error: it means "no assume-role hop" — the caller then uses ambient
// credentials, which is the correct behavior for the local CLI run directly
// with customer creds.
func ResolveAWSProviderAuth(outputDir string) (AWSProviderAuth, error) {
	auth := AWSProviderAuth{
		RoleARN:    strings.TrimSpace(os.Getenv(tfEnvPrefix + tfVarBootstrapRole)),
		ExternalID: strings.TrimSpace(os.Getenv(tfEnvPrefix + tfVarAWSExternalID)),
	}

	root := findProjectRoot(outputDir)
	if root == "" {
		return auth, nil
	}

	if auth.RoleARN == "" {
		roleARN, err := terraformOutputString(filepath.Join(root, "outputs", "cloud-provision.json"), "terraform_role")
		if err != nil {
			return auth, err
		}
		auth.RoleARN = roleARN
	}

	autoVarsDir := filepath.Join(root, "tf", "auto-vars")
	if auth.RoleARN == "" {
		roleARN, err := autoVarString(autoVarsDir, tfVarBootstrapRole)
		if err != nil {
			return auth, err
		}
		auth.RoleARN = roleARN
	}
	if auth.ExternalID == "" {
		externalID, err := autoVarString(autoVarsDir, tfVarAWSExternalID)
		if err != nil {
			return auth, err
		}
		auth.ExternalID = externalID
	}

	return auth, nil
}

func findProjectRoot(outputDir string) string {
	if strings.TrimSpace(outputDir) == "" {
		return ""
	}
	dir, err := filepath.Abs(outputDir)
	if err != nil {
		dir = outputDir
	}
	for {
		if pathExists(filepath.Join(dir, "tf", "auto-vars")) ||
			pathExists(filepath.Join(dir, "outputs", "cloud-provision.json")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func autoVarString(dir, key string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		value, ok, err := jsonFileString(path, key)
		if err != nil {
			return "", err
		}
		if ok {
			return value, nil
		}
	}
	return "", nil
}

func terraformOutputString(path, key string) (string, error) {
	if !pathExists(path) {
		return "", nil
	}
	value, _, err := jsonFileString(path, key)
	return value, err
}

func jsonFileString(path, key string) (string, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &values); err != nil {
		return "", false, fmt.Errorf("parse %s: %w", path, err)
	}
	raw, ok := values[key]
	if !ok || len(raw) == 0 {
		return "", false, nil
	}
	value, ok, err := decodeStringValue(raw)
	if err != nil {
		return "", false, fmt.Errorf("decode %s %q: %w", path, key, err)
	}
	return value, ok, nil
}

func decodeStringValue(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		return s, s != "", nil
	}

	var wrapped struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || len(wrapped.Value) == 0 {
		return "", false, fmt.Errorf("expected string or Terraform output object")
	}
	return decodeStringValue(wrapped.Value)
}
