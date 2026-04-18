package composer

import "testing"

func TestHumanizeFieldValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field string
		value string
		want  string
	}{
		// Booleans
		{"ha", "true", "Yes"},
		{"ha", "false", "No"},
		{"versioning", "true", "Yes"},
		{"multiAz", "false", "No"},
		{"ha", "", ""},

		// Durations - shorthand
		{"defaultTtl", "1h", "1 hour"},
		{"defaultTtl", "24h", "24 hours"},
		{"defaultTtl", "1day", "1 day"},
		{"retentionPeriod", "7day", "7 days"},
		{"visibilityTimeout", "30s", "30 seconds"},
		{"timeout", "5m", "5 minutes"},

		// Durations - plain seconds
		{"defaultTtl", "86400", "1 day"},
		{"defaultTtl", "3600", "1 hour"},
		{"timeout", "30", "30 seconds"},
		{"timeout", "300", "5 minutes"},

		// Durations - passthrough
		{"defaultTtl", "forever", "forever"},

		// Retention days
		{"retentionDays", "30", "30 days"},
		{"retentionDays", "90", "90 days"},
		{"retentionDays", "unlimited", "unlimited"},

		// Storage sizes
		{"storageSize", "100", "100 GB"},
		{"diskSizePerServer", "20", "20 GB"},
		{"diskSizeGb", "50", "50 GB"},
		{"storageSize", "100GB", "100GB"},

		// Enum maps
		{"type", "provisioned", "Provisioned"},
		{"type", "pay_per_request", "Pay Per Request"},
		{"type", "standard", "Standard"},
		{"type", "fifo", "FIFO"},
		{"deploymentType", "single_node", "Single Node"},
		{"controlPlaneVisibility", "public", "Public"},
		{"storageClass", "STANDARD", "Standard"},
		{"type", "unknown_type", "unknown_type"},

		// Passthrough
		{"instanceType", "db.r5.large", "db.r5.large"},
		{"machineType", "e2-medium", "e2-medium"},
		{"cpuSize", "db.t4.medium", "db.t4.medium"},
		{"numServers", "3", "3"},
		{"desiredSize", "5", "5"},
		{"unknownKey", "whatever", "whatever"},
	}

	for _, tt := range tests {
		t.Run(tt.field+"_"+tt.value, func(t *testing.T) {
			t.Parallel()
			got := HumanizeFieldValue(tt.field, tt.value)
			if got != tt.want {
				t.Errorf("HumanizeFieldValue(%q, %q) = %q, want %q", tt.field, tt.value, got, tt.want)
			}
		})
	}
}
