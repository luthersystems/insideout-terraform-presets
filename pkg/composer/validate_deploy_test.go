package composer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateDeployConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		oldCfg         Config
		newCfg         Config
		wantErr        bool
		wantViolations int      // expected number of violations (semicolon-separated)
		errMsgs        []string // substrings expected in error
	}{
		// --- AWS EC2 ---
		{
			name:    "valid: EC2 no change",
			oldCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"}}`),
			newCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"}}`),
			wantErr: false,
		},
		{
			name:    "valid: EC2 disk increase",
			oldCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"50"}}`),
			newCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"}}`),
			wantErr: false,
		},
		{
			name:           "invalid: EC2 disk decrease",
			oldCfg:         cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"}}`),
			newCfg:         cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"50"}}`),
			wantErr:        true,
			wantViolations: 1,
			errMsgs:        []string{"cannot shrink aws_ec2 disk", "100 GB", "50 GB", "EBS volumes"},
		},
		{
			name:    "valid: EC2 old nil (first deploy)",
			oldCfg:  Config{},
			newCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"}}`),
			wantErr: false,
		},
		{
			name:    "valid: EC2 new nil (component removed)",
			oldCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"}}`),
			newCfg:  Config{},
			wantErr: false,
		},
		{
			name:    "valid: EC2 old disk empty string",
			oldCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":""}}`),
			newCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"50"}}`),
			wantErr: false,
		},
		{
			name:    "valid: EC2 new disk empty string",
			oldCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"}}`),
			newCfg:  cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":""}}`),
			wantErr: false,
		},

		// --- AWS RDS ---
		{
			name:           "invalid: RDS storage decrease",
			oldCfg:         cfgFromJSON(t, `{"aws_rds":{"storageSize":"200"}}`),
			newCfg:         cfgFromJSON(t, `{"aws_rds":{"storageSize":"100"}}`),
			wantErr:        true,
			wantViolations: 1,
			errMsgs:        []string{"cannot shrink aws_rds storage", "200 GB", "100 GB", "RDS storage"},
		},
		{
			name:    "valid: RDS storage increase",
			oldCfg:  cfgFromJSON(t, `{"aws_rds":{"storageSize":"100"}}`),
			newCfg:  cfgFromJSON(t, `{"aws_rds":{"storageSize":"200"}}`),
			wantErr: false,
		},

		// --- AWS ElastiCache ---
		{
			name:           "invalid: ElastiCache storage decrease",
			oldCfg:         cfgFromJSON(t, `{"aws_elasticache":{"storageSize":"100"}}`),
			newCfg:         cfgFromJSON(t, `{"aws_elasticache":{"storageSize":"50"}}`),
			wantErr:        true,
			wantViolations: 1,
			errMsgs:        []string{"cannot shrink aws_elasticache storage", "100 GB", "50 GB", "ElastiCache storage"},
		},
		{
			name:    "valid: ElastiCache storage increase",
			oldCfg:  cfgFromJSON(t, `{"aws_elasticache":{"storageSize":"50"}}`),
			newCfg:  cfgFromJSON(t, `{"aws_elasticache":{"storageSize":"100"}}`),
			wantErr: false,
		},

		// --- AWS OpenSearch ---
		{
			name:           "invalid: OpenSearch storage decrease",
			oldCfg:         cfgFromJSON(t, `{"aws_opensearch":{"storageSize":"200"}}`),
			newCfg:         cfgFromJSON(t, `{"aws_opensearch":{"storageSize":"100"}}`),
			wantErr:        true,
			wantViolations: 1,
			errMsgs:        []string{"cannot shrink aws_opensearch storage", "200 GB", "100 GB", "OpenSearch storage"},
		},
		{
			name:    "valid: OpenSearch storage increase",
			oldCfg:  cfgFromJSON(t, `{"aws_opensearch":{"storageSize":"100"}}`),
			newCfg:  cfgFromJSON(t, `{"aws_opensearch":{"storageSize":"200"}}`),
			wantErr: false,
		},

		// --- GCP Compute ---
		{
			name:           "invalid: GCP compute disk decrease",
			oldCfg:         cfgFromJSON(t, `{"gcp_compute":{"diskSizeGb":100}}`),
			newCfg:         cfgFromJSON(t, `{"gcp_compute":{"diskSizeGb":50}}`),
			wantErr:        true,
			wantViolations: 1,
			errMsgs:        []string{"cannot shrink gcp_compute disk", "100 GB", "50 GB", "persistent disks"},
		},
		{
			name:    "valid: GCP compute disk increase",
			oldCfg:  cfgFromJSON(t, `{"gcp_compute":{"diskSizeGb":50}}`),
			newCfg:  cfgFromJSON(t, `{"gcp_compute":{"diskSizeGb":100}}`),
			wantErr: false,
		},
		{
			name:    "valid: GCP compute old zero (unset)",
			oldCfg:  cfgFromJSON(t, `{"gcp_compute":{"diskSizeGb":0}}`),
			newCfg:  cfgFromJSON(t, `{"gcp_compute":{"diskSizeGb":100}}`),
			wantErr: false,
		},
		{
			name:    "valid: GCP compute new zero (unset/omitted)",
			oldCfg:  cfgFromJSON(t, `{"gcp_compute":{"diskSizeGb":100}}`),
			newCfg:  cfgFromJSON(t, `{"gcp_compute":{}}`),
			wantErr: false,
		},

		// --- GCP CloudSQL ---
		{
			name:           "invalid: GCP CloudSQL disk decrease",
			oldCfg:         cfgFromJSON(t, `{"gcp_cloudsql":{"diskSizeGb":100}}`),
			newCfg:         cfgFromJSON(t, `{"gcp_cloudsql":{"diskSizeGb":50}}`),
			wantErr:        true,
			wantViolations: 1,
			errMsgs:        []string{"cannot shrink gcp_cloudsql disk", "100 GB", "50 GB", "Cloud SQL storage"},
		},
		{
			name:    "valid: GCP CloudSQL disk increase",
			oldCfg:  cfgFromJSON(t, `{"gcp_cloudsql":{"diskSizeGb":50}}`),
			newCfg:  cfgFromJSON(t, `{"gcp_cloudsql":{"diskSizeGb":100}}`),
			wantErr: false,
		},
		{
			name:    "valid: GCP CloudSQL new zero (unset/omitted)",
			oldCfg:  cfgFromJSON(t, `{"gcp_cloudsql":{"diskSizeGb":100}}`),
			newCfg:  cfgFromJSON(t, `{"gcp_cloudsql":{}}`),
			wantErr: false,
		},

		// --- Multiple violations ---
		{
			name:           "invalid: multiple AWS violations (EC2 + RDS)",
			oldCfg:         cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"},"aws_rds":{"storageSize":"200"}}`),
			newCfg:         cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"50"},"aws_rds":{"storageSize":"100"}}`),
			wantErr:        true,
			wantViolations: 2,
			errMsgs:        []string{"aws_ec2 disk", "aws_rds storage"},
		},
		{
			name:           "invalid: cross-cloud violations (EC2 + GCP CloudSQL)",
			oldCfg:         cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"100"},"gcp_cloudsql":{"diskSizeGb":100}}`),
			newCfg:         cfgFromJSON(t, `{"aws_ec2":{"diskSizePerServer":"50"},"gcp_cloudsql":{"diskSizeGb":50}}`),
			wantErr:        true,
			wantViolations: 2,
			errMsgs:        []string{"aws_ec2 disk", "gcp_cloudsql disk"},
		},

		// --- Edge cases ---
		{
			name:    "valid: both configs empty",
			oldCfg:  Config{},
			newCfg:  Config{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDeployConstraints(tt.oldCfg, tt.newCfg)
			if tt.wantErr {
				require.Error(t, err)
				for _, msg := range tt.errMsgs {
					require.Contains(t, err.Error(), msg)
				}
				if tt.wantViolations > 0 {
					got := strings.Count(err.Error(), "cannot shrink")
					require.Equal(t, tt.wantViolations, got,
						"expected %d violations but got %d in: %s", tt.wantViolations, got, err.Error())
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckDiskShrinkStr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		label  string
		oldVal string
		newVal string
		reason string
		want   string // "" means no violation
	}{
		{
			name:   "shrink detected",
			label:  "test disk", oldVal: "100", newVal: "50", reason: "cannot reduce",
			want: "cannot shrink test disk from 100 GB to 50 GB — cannot reduce",
		},
		{
			name:   "increase — no violation",
			label:  "test disk", oldVal: "50", newVal: "100", reason: "n/a",
			want: "",
		},
		{
			name:   "equal — no violation",
			label:  "test disk", oldVal: "100", newVal: "100", reason: "n/a",
			want: "",
		},
		{
			name:   "old empty — skip",
			label:  "test disk", oldVal: "", newVal: "50", reason: "n/a",
			want: "",
		},
		{
			name:   "new empty — skip",
			label:  "test disk", oldVal: "100", newVal: "", reason: "n/a",
			want: "",
		},
		{
			name:   "old unparseable — skip",
			label:  "test disk", oldVal: "abc", newVal: "50", reason: "n/a",
			want: "",
		},
		{
			name:   "new unparseable — skip",
			label:  "test disk", oldVal: "100", newVal: "abc", reason: "n/a",
			want: "",
		},
		{
			name:   "old zero — skip",
			label:  "test disk", oldVal: "0", newVal: "50", reason: "n/a",
			want: "",
		},
		{
			name:   "new zero — skip (treated as unset)",
			label:  "test disk", oldVal: "100", newVal: "0", reason: "n/a",
			want: "",
		},
		{
			name:   "old negative — skip",
			label:  "test disk", oldVal: "-5", newVal: "50", reason: "n/a",
			want: "",
		},
		{
			name:   "new negative — skip",
			label:  "test disk", oldVal: "100", newVal: "-5", reason: "n/a",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkDiskShrinkStr(tt.label, tt.oldVal, tt.newVal, tt.reason)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseDiskSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"100", 100, false},
		{"0", 0, false},
		{"", 0, true},
		{"  50  ", 50, false},
		{"abc", 0, true},
		{"-5", -5, false}, // strconv.Atoi parses negatives; caller checks <= 0
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := parseDiskSize(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}
