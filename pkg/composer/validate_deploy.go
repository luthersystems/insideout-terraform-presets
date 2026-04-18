package composer

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateDeployConstraints checks that a config change does not violate
// irreversible cloud constraints (e.g. EBS volumes and RDS storage cannot
// be shrunk). Returns a multi-error listing all violations, or nil if the
// change is safe.
func ValidateDeployConstraints(oldCfg, newCfg Config) error {
	var violations []string

	// AWS EC2 disk size (string, in GB)
	if oldCfg.AWSEC2 != nil && newCfg.AWSEC2 != nil {
		if err := checkDiskShrinkStr("aws_ec2 disk", oldCfg.AWSEC2.DiskSizePerServer, newCfg.AWSEC2.DiskSizePerServer, "EBS volumes cannot be reduced"); err != "" {
			violations = append(violations, err)
		}
	}

	// AWS RDS storage size (string, in GB)
	if oldCfg.AWSRDS != nil && newCfg.AWSRDS != nil {
		if err := checkDiskShrinkStr("aws_rds storage", oldCfg.AWSRDS.StorageSize, newCfg.AWSRDS.StorageSize, "RDS storage cannot be reduced"); err != "" {
			violations = append(violations, err)
		}
	}

	// AWS ElastiCache storage size (string, in GB)
	if oldCfg.AWSElastiCache != nil && newCfg.AWSElastiCache != nil {
		if err := checkDiskShrinkStr("aws_elasticache storage", oldCfg.AWSElastiCache.Storage, newCfg.AWSElastiCache.Storage, "ElastiCache storage cannot be reduced"); err != "" {
			violations = append(violations, err)
		}
	}

	// AWS OpenSearch storage size (string, in GB)
	if oldCfg.AWSOpenSearch != nil && newCfg.AWSOpenSearch != nil {
		if err := checkDiskShrinkStr("aws_opensearch storage", oldCfg.AWSOpenSearch.StorageSize, newCfg.AWSOpenSearch.StorageSize, "OpenSearch storage cannot be reduced"); err != "" {
			violations = append(violations, err)
		}
	}

	// GCP Compute disk size (int, in GB)
	if oldCfg.GCPCompute != nil && newCfg.GCPCompute != nil {
		if msg := checkDiskShrinkInt("gcp_compute disk", oldCfg.GCPCompute.DiskSizeGb, newCfg.GCPCompute.DiskSizeGb, "persistent disks cannot be reduced"); msg != "" {
			violations = append(violations, msg)
		}
	}

	// GCP Cloud SQL disk size (int, in GB)
	if oldCfg.GCPCloudSQL != nil && newCfg.GCPCloudSQL != nil {
		if msg := checkDiskShrinkInt("gcp_cloudsql disk", oldCfg.GCPCloudSQL.DiskSizeGb, newCfg.GCPCloudSQL.DiskSizeGb, "Cloud SQL storage cannot be reduced"); msg != "" {
			violations = append(violations, msg)
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(violations, "; "))
}

// checkDiskShrinkInt compares two integer disk sizes (GB). Returns a
// non-empty violation message if newVal < oldVal. Zero values are treated
// as unset (e.g. JSON omitempty default) and silently skipped.
func checkDiskShrinkInt(label string, oldVal, newVal int, reason string) string {
	if oldVal <= 0 || newVal <= 0 {
		return ""
	}
	if newVal < oldVal {
		return fmt.Sprintf("cannot shrink %s from %d GB to %d GB — %s", label, oldVal, newVal, reason)
	}
	return ""
}

// checkDiskShrinkStr compares two string-encoded disk sizes. Returns a
// non-empty violation message if newVal < oldVal. Empty strings or
// unparseable values are silently skipped (no violation).
func checkDiskShrinkStr(label, oldVal, newVal, reason string) string {
	oldSize, err := parseDiskSize(oldVal)
	if err != nil || oldSize <= 0 {
		return ""
	}
	newSize, err := parseDiskSize(newVal)
	if err != nil || newSize <= 0 {
		return ""
	}
	if newSize < oldSize {
		return fmt.Sprintf("cannot shrink %s from %d GB to %d GB — %s", label, oldSize, newSize, reason)
	}
	return ""
}

// parseDiskSize converts a string disk size to an integer (GB).
// Returns an error for empty strings or non-numeric values.
func parseDiskSize(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty disk size")
	}
	return strconv.Atoi(s)
}
