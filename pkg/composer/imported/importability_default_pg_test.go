package imported

import "testing"

// Pins the AWS-managed default parameter group classification (the
// "Tagging on default resources is not supported" apply failure) and the
// compose-side DropUnimportable pass.
func TestUnimportableReason_AWSDefaultParameterGroups(t *testing.T) {
	mk := func(tfType, importID string) ImportedResource {
		return ImportedResource{Identity: ResourceIdentity{Type: tfType, Address: tfType + ".x", ImportID: importID}}
	}
	cases := []struct {
		name string
		ir   ImportedResource
		want string
	}{
		{"elasticache default", mk("aws_elasticache_parameter_group", "default.memcached1.5"), ReasonAWSDefaultParameterGroup},
		{"elasticache valkey default", mk("aws_elasticache_parameter_group", "default.valkey9.cluster.on"), ReasonAWSDefaultParameterGroup},
		{"rds default", mk("aws_db_parameter_group", "default.postgres16"), ReasonAWSDefaultParameterGroup},
		{"rds cluster default", mk("aws_rds_cluster_parameter_group", "default.aurora-postgresql15"), ReasonAWSDefaultParameterGroup},
		{"custom elasticache group importable", mk("aws_elasticache_parameter_group", "io-abc-redis7"), ""},
		{"default prefix on unrelated type importable", mk("aws_sqs_queue", "default.something"), ""},
		{"name from NameHint fallback", ImportedResource{Identity: ResourceIdentity{Type: "aws_elasticache_parameter_group", Address: "a.b", NameHint: "default.redis7"}}, ReasonAWSDefaultParameterGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UnimportableReason(tc.ir); got != tc.want {
				t.Fatalf("UnimportableReason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDropUnimportable(t *testing.T) {
	def := ImportedResource{Identity: ResourceIdentity{Type: "aws_elasticache_parameter_group", Address: "aws_elasticache_parameter_group.d", ImportID: "default.memcached1.5"}}
	custom := ImportedResource{Identity: ResourceIdentity{Type: "aws_elasticache_parameter_group", Address: "aws_elasticache_parameter_group.c", ImportID: "io-x-redis"}}
	kept, dropped := DropUnimportable([]ImportedResource{def, custom})
	if len(kept) != 1 || kept[0].Identity.Address != "aws_elasticache_parameter_group.c" {
		t.Fatalf("kept = %+v", kept)
	}
	if len(dropped) != 1 || dropped[0][0] != "aws_elasticache_parameter_group.d" || dropped[0][1] != ReasonAWSDefaultParameterGroup {
		t.Fatalf("dropped = %+v", dropped)
	}
	if desc := ReasonDescription(ReasonAWSDefaultParameterGroup); desc == "" {
		t.Fatal("ReasonDescription must cover the new reason")
	}
}
