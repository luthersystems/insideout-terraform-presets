package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoverModuleOutputs_BasicParsing(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"/outputs.tf": []byte(`
output "instance_id" {
  value       = aws_instance.this.id
  description = "The ID of the EC2 instance"
}

output "public_ip" {
  value       = aws_eip.this.public_ip
  description = "The public IP address"
}

output "db_password" {
  value       = random_password.db.result
  description = "The database password"
  sensitive   = true
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err)
	require.Len(t, outputs, 3)

	// Sorted alphabetically
	require.Equal(t, "db_password", outputs[0].Name)
	require.Equal(t, "The database password", outputs[0].Description)
	require.True(t, outputs[0].Sensitive)

	require.Equal(t, "instance_id", outputs[1].Name)
	require.Equal(t, "The ID of the EC2 instance", outputs[1].Description)
	require.False(t, outputs[1].Sensitive)

	require.Equal(t, "public_ip", outputs[2].Name)
	require.Equal(t, "The public IP address", outputs[2].Description)
	require.False(t, outputs[2].Sensitive)
}

func TestDiscoverModuleOutputs_NoOutputs(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"/main.tf": []byte(`
resource "aws_instance" "this" {
  ami           = "ami-12345"
  instance_type = "t3.micro"
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err)
	require.Empty(t, outputs)
}

func TestDiscoverModuleOutputs_SkipsNonTF(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"/README.md": []byte(`output "fake" { value = "nope" }`),
		"/outputs.tf": []byte(`
output "real" {
  value = "yes"
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, "real", outputs[0].Name)
}

func TestDiscoverModuleOutputs_MultipleFiles(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"/outputs.tf": []byte(`
output "vpc_id" {
  value       = module.this.vpc_id
  description = "VPC ID"
}
`),
		"/extra_outputs.tf": []byte(`
output "subnet_ids" {
  value       = module.this.subnet_ids
  description = "Subnet IDs"
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err)
	require.Len(t, outputs, 2)

	require.Equal(t, "subnet_ids", outputs[0].Name)
	require.Equal(t, "vpc_id", outputs[1].Name)
}

func TestDiscoverModuleOutputs_NoDescription(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"/outputs.tf": []byte(`
output "bare" {
  value = "hello"
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, "bare", outputs[0].Name)
	require.Equal(t, "", outputs[0].Description)
	require.False(t, outputs[0].Sensitive)
}

func TestDiscoverModuleOutputs_SensitiveFalse(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"/outputs.tf": []byte(`
output "public_info" {
  value     = "hello"
  sensitive = false
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.False(t, outputs[0].Sensitive)
}

func TestDiscoverModuleOutputs_MalformedHCLSkipped(t *testing.T) {
	t.Parallel()

	files := map[string][]byte{
		"/broken.tf": []byte(`this is not valid HCL { {{{{ `),
		"/outputs.tf": []byte(`
output "good" {
  value       = "works"
  description = "Valid output"
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err, "malformed files should be silently skipped")
	require.Len(t, outputs, 1)
	require.Equal(t, "good", outputs[0].Name)
	require.Equal(t, "Valid output", outputs[0].Description)
}

func TestDiscoverModuleOutputs_EmptyMap(t *testing.T) {
	t.Parallel()

	outputs, err := DiscoverModuleOutputs(map[string][]byte{})
	require.NoError(t, err)
	require.Empty(t, outputs)
}

func TestDiscoverModuleOutputs_InterpolatedDescription(t *testing.T) {
	t.Parallel()

	// Descriptions with interpolation won't be extracted (only literal strings),
	// but the output should still be discovered with an empty description.
	files := map[string][]byte{
		"/outputs.tf": []byte(`
variable "env" {
  default = "prod"
}

output "dynamic_desc" {
  value       = "something"
  description = "The ${var.env} endpoint"
}

output "literal_desc" {
  value       = "other"
  description = "A plain description"
}
`),
	}

	outputs, err := DiscoverModuleOutputs(files)
	require.NoError(t, err)
	require.Len(t, outputs, 2)

	// dynamic_desc: interpolated description won't parse as literal string
	require.Equal(t, "dynamic_desc", outputs[0].Name)
	// literal_desc: plain string extracts fine
	require.Equal(t, "literal_desc", outputs[1].Name)
	require.Equal(t, "A plain description", outputs[1].Description)
}
