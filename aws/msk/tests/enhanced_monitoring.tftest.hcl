mock_provider "aws" {}

# Regression for #95 (phase 2c). MSK's aws_msk_cluster.enhanced_monitoring
# maps directly onto AWS/Kafka CloudWatch metric fan-out:
#
#   DEFAULT              -> 6 broker-level metrics (cluster aggregates only)
#   PER_BROKER           -> ~23 broker metrics (CPU/mem/net/disk per broker)
#   PER_TOPIC_PER_BROKER -> PER_BROKER + per-topic throughput & lag
#
# Default must be PER_BROKER so reliable2 panels charting broker-level CPU,
# memory, network, and disk populate without any override — same "on by
# default" principle as every other preset in this umbrella. If someone
# reverts to DEFAULT to trim cost they should do it via an explicit
# override in the stack config, not by flipping the module default back.

run "default_enhanced_monitoring_is_per_broker" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    vpc_id      = "vpc-12345"
    subnet_ids  = ["subnet-aaa", "subnet-bbb", "subnet-ccc"]
  }

  assert {
    condition     = aws_msk_cluster.this.enhanced_monitoring == "PER_BROKER"
    error_message = "Default enhanced_monitoring must be PER_BROKER — dropping back to DEFAULT silently loses broker-level CPU/memory/network/disk metrics that reliable2 panels chart."
  }
}

run "enhanced_monitoring_override_wins" {
  command = plan

  variables {
    project             = "test"
    region              = "us-east-1"
    environment         = "test"
    vpc_id              = "vpc-12345"
    subnet_ids          = ["subnet-aaa", "subnet-bbb", "subnet-ccc"]
    enhanced_monitoring = "DEFAULT"
  }

  assert {
    condition     = aws_msk_cluster.this.enhanced_monitoring == "DEFAULT"
    error_message = "Caller override (var.enhanced_monitoring) must reach the cluster — otherwise cost-sensitive callers can't opt out."
  }
}
