package composer

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

var (
	printGenerated bool
	writeOutDir    string
)

func TestMain(m *testing.M) {
	flag.BoolVar(&printGenerated, "print", false, "print generated files to stdout")
	flag.StringVar(&writeOutDir, "outdir", "", "write generated files to this directory (will be created)")
	flag.Parse()
	os.Exit(m.Run())
}

func parseHCL(name string, b []byte) error {
	_, diags := hclwrite.ParseConfig(b, name, hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("%s: %s", name, diags.Error())
	}
	return nil
}

func writeBundle(t *testing.T, base string, files Files) {
	t.Helper()
	require.NotEmpty(t, base, "writeBundle base dir is empty")
	err := os.MkdirAll(base, 0o750)
	require.NoError(t, err, "mkdir outdir")

	for p, b := range files {
		full := filepath.Join(base, strings.TrimPrefix(p, "/"))
		err := os.MkdirAll(filepath.Dir(full), 0o750)
		require.NoError(t, err, "mkdir for %s", full)
		err = os.WriteFile(full, b, 0o600)
		require.NoError(t, err, "write file %s", full)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestComposeSingle_VM_WithTestify(t *testing.T) {
	// Ensure eks_nodegroup preset exists in the embedded FS (KeyAWSEKSNodeGroup maps to aws/eks_nodegroup)
	presetFiles, err := newTestClient().GetPresetFiles("aws/eks_nodegroup")
	require.NoError(t, err, "GetPresetFiles(aws/eks_nodegroup)")
	require.NotEmpty(t, presetFiles, "aws/eks_nodegroup preset should not be empty")
	_, hasVars := presetFiles["/variables.tf"]
	_, hasMain := presetFiles["/main.tf"]
	require.True(t, hasVars && hasMain, "aws/eks_nodegroup preset should include variables.tf and main.tf")

	// Compose
	c := newTestClient(WithTerraformVersion("1.7.5"))
	out, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud:   "aws",
		Key:     KeyAWSEKSNodeGroup,
		Comps:   &Components{},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err, "ComposeSingle(vm)")
	require.NotEmpty(t, out, "composed bundle should not be empty")

	// Validate required root files
	mainTF, okMain := out["/main.tf"]
	varsTF, okVars := out["/variables.tf"]
	tfVer, okVer := out["/.terraform-version"]
	require.True(t, okMain, "expected /main.tf in output; got keys: %v", keysOf(out))
	require.True(t, okVars, "expected /variables.tf in output; got keys: %v", keysOf(out))
	require.True(t, okVer, "expected /.terraform-version in output; got keys: %v", keysOf(out))
	require.NotEmpty(t, bytes.TrimSpace(tfVer), ".terraform-version must not be empty")

	// Rebased preset should exist under modules/eks_nodegroup (KeyAWSEKSNodeGroup is EKS managed node group)
	_, ok := out["/modules/eks_nodegroup/variables.tf"]
	assert.True(t, ok, "expected /modules/eks_nodegroup/variables.tf; got keys: %v", keysOf(out))
	_, ok = out["/modules/eks_nodegroup/main.tf"]
	assert.True(t, ok, "expected /modules/eks_nodegroup/main.tf; got keys: %v", keysOf(out))

	// Parse HCL for generated files
	require.NoError(t, parseHCL("main.tf", mainTF), "main.tf should parse")
	require.NoError(t, parseHCL("variables.tf", varsTF), "variables.tf should parse")

	// main.tf should contain module block and wire project/region to namespaced vars
	mainStr := string(mainTF)
	assert.Contains(t, mainStr, `module "ec2"`, `main.tf should contain module "ec2" block`)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*project\s*=\s*var\.ec2_project\s*$`), mainStr,
		"project should be wired as var.ec2_project")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*region\s*=\s*var\.ec2_region\s*$`), mainStr,
		"region should be wired as var.ec2_region")

	// variables.tf should declare namespaced vars
	varsStr := string(varsTF)
	assert.Contains(t, varsStr, `variable "ec2_project"`, `variables.tf should declare "ec2_project"`)
	assert.Contains(t, varsStr, `variable "ec2_region"`, `variables.tf should declare "ec2_region"`)

	// ec2.auto.tfvars should include mapper-provided values with namespaced keys (allow aligned spacing)
	tfvars, ok := out["/ec2.auto.tfvars"]
	require.True(t, ok, "expected /ec2.auto.tfvars in output")
	tfvarsStr := string(tfvars)
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*ec2_project\s*=\s*"demo"\s*$`), tfvarsStr,
		"/ec2.auto.tfvars should contain ec2_project (namespaced)")
	require.Regexp(t, regexp.MustCompile(`(?m)^\s*ec2_region\s*=\s*"us-east-1"\s*$`), tfvarsStr,
		"/ec2.auto.tfvars should contain ec2_region (namespaced)")

	// Optional: print and/or write files for manual inspection
	if printGenerated {
		t.Log("---- Generated files ----")
		names := keysOf(out)
		for _, p := range names {
			t.Logf("== %s ==\n%s\n", p, string(out[p]))
		}
	}
	if writeOutDir != "" {
		writeBundle(t, writeOutDir, out)
		t.Logf("Wrote generated bundle to: %s", writeOutDir)
	}
}

func TestListClouds(t *testing.T) {
	clouds, err := newTestClient().ListClouds()
	require.NoError(t, err, "ListClouds should succeed")
	require.Contains(t, clouds, "aws", "should contain aws")
	require.Contains(t, clouds, "gcp", "should contain gcp")
}

func TestListPresetKeysForCloud(t *testing.T) {
	// Test AWS modules
	c := newTestClient()
	awsKeys, err := c.ListPresetKeysForCloud("aws")
	require.NoError(t, err, "ListPresetKeysForCloud(aws) should succeed")
	require.Contains(t, awsKeys, "aws/vpc", "AWS should have vpc module")
	require.Contains(t, awsKeys, "aws/ec2", "AWS should have ec2 module (standalone)")
	require.Contains(t, awsKeys, "aws/eks_nodegroup", "AWS should have eks_nodegroup module")
	require.Contains(t, awsKeys, "aws/rds", "AWS should have rds module")
	require.Contains(t, awsKeys, "aws/s3", "AWS should have s3 module")

	// Test GCP modules
	gcpKeys, err := c.ListPresetKeysForCloud("gcp")
	require.NoError(t, err, "ListPresetKeysForCloud(gcp) should succeed")
	require.Contains(t, gcpKeys, "gcp/vpc", "GCP should have vpc module")
	require.Contains(t, gcpKeys, "gcp/compute", "GCP should have compute module")
	require.Contains(t, gcpKeys, "gcp/cloudsql", "GCP should have cloudsql module")
	require.Contains(t, gcpKeys, "gcp/gcs", "GCP should have gcs module")
	require.Contains(t, gcpKeys, "gcp/gke", "GCP should have gke module")
	require.Contains(t, gcpKeys, "gcp/loadbalancer", "GCP should have loadbalancer module")
	require.Contains(t, gcpKeys, "gcp/kms", "GCP should have kms module")
	require.Contains(t, gcpKeys, "gcp/secretmanager", "GCP should have secretmanager module")
}

func TestGetPresetFiles_GCP_VPC(t *testing.T) {
	// Ensure GCP VPC preset exists in the embedded FS
	presetFiles, err := newTestClient().GetPresetFiles("gcp/vpc")
	require.NoError(t, err, "GetPresetFiles(gcp/vpc)")
	require.NotEmpty(t, presetFiles, "gcp/vpc preset should not be empty")
	_, hasVars := presetFiles["/variables.tf"]
	_, hasMain := presetFiles["/main.tf"]
	require.True(t, hasVars && hasMain, "gcp/vpc preset should include variables.tf and main.tf")

	// Parse HCL for generated files
	for name, content := range presetFiles {
		if strings.HasSuffix(name, ".tf") {
			require.NoError(t, parseHCL(name, content), "%s should parse as valid HCL", name)
		}
	}
}

func TestGetPresetFiles_GCP_AllModules(t *testing.T) {
	gcpModules := []string{
		"gcp/vpc",
		"gcp/compute",
		"gcp/gke",
		"gcp/cloudsql",
		"gcp/gcs",
		"gcp/loadbalancer",
		"gcp/kms",
		"gcp/secretmanager",
	}

	for _, mod := range gcpModules {
		t.Run(mod, func(t *testing.T) {
			files, err := newTestClient().GetPresetFiles(mod)
			require.NoError(t, err, "GetPresetFiles(%s)", mod)
			require.NotEmpty(t, files, "%s preset should not be empty", mod)

			// Validate required files exist
			_, hasVars := files["/variables.tf"]
			_, hasMain := files["/main.tf"]
			require.True(t, hasVars, "%s should have variables.tf", mod)
			require.True(t, hasMain, "%s should have main.tf", mod)

			// Parse all .tf files as valid HCL
			for name, content := range files {
				if strings.HasSuffix(name, ".tf") {
					require.NoError(t, parseHCL(name, content), "%s/%s should parse as valid HCL", mod, name)
				}
			}
		})
	}
}
