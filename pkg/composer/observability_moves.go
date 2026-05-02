package composer

// observabilityMoves declares the moved {} block address pairs the
// composer emits when a per-component module that owns observability
// alarms is selected alongside aws_cloudwatchmonitoring. Each entry
// relocates a legacy aggregator-side alarm (in module
// "aws_cloudwatchmonitoring") into the per-component module's own
// observability.tf so existing customer state continues to refer to a
// real resource without forcing destroy+create.
//
// Source-key shape: every legacy alarm in
// aws/cloudwatchmonitoring/main.tf uses
// `for_each = { for i in tolist(range(length(var.X))) : i => true }`,
// which produces stringified-int keys ("0", "1", ...). Destination
// addresses MUST also use stringified-int for_each keys so the
// relocation is a no-op rather than a destroy+create. Per-component
// observability.tf files (lands in C7) use
// `for_each = var.enable_observability ? { "0" = true } : {}` to
// produce the same `["0"]` address shape on the destination side.
//
// Aggregator wiring footnotes (see DefaultWiring's
// `case KeyAWSCloudWatchMonitoring` arm in pkg/composer/contracts.go for
// the legacy aggregator wiring):
//
//   - ec2_cpu_high targets `instance_ids` which is wired ONLY from
//     bastion (NOT from aws_ec2 today). So the source maps cleanly to
//     a single per-bastion destination.
//   - redis_cpu_high targets `elasticache_replication_group_ids` which
//     is NOT wired anywhere today — the legacy alarm is effectively a
//     dormant resource. The move entry is declared anyway so a future
//     wiring change can rely on the relocation infrastructure.
//   - rds_cpu_high / rds_free_storage_low / sqs_backlog target their
//     respective single-instance modules.
//
// The destination-side per-component alarm resource names are stable
// and live in <module>/observability.tf. Renaming a destination here
// without renaming it in the corresponding observability.tf will fail
// the TestObservabilityMoves_DestinationsMatchPresets gate added in C9.
//
// observabilityMoves is keyed by ComponentKey (the destination's
// component) so compose.go can populate ModuleBlock.Moved by table
// lookup.
var observabilityMoves = map[ComponentKey][]MovedRef{
	KeyAWSBastion: {{
		From: `module.aws_cloudwatchmonitoring.aws_cloudwatch_metric_alarm.ec2_cpu_high["0"]`,
		To:   `module.aws_bastion.aws_cloudwatch_metric_alarm.cpu_high["0"]`,
	}},
	KeyAWSRDS: {
		{
			From: `module.aws_cloudwatchmonitoring.aws_cloudwatch_metric_alarm.rds_cpu_high["0"]`,
			To:   `module.aws_rds.aws_cloudwatch_metric_alarm.cpu_high["0"]`,
		},
		{
			From: `module.aws_cloudwatchmonitoring.aws_cloudwatch_metric_alarm.rds_free_storage_low["0"]`,
			To:   `module.aws_rds.aws_cloudwatch_metric_alarm.free_storage_low["0"]`,
		},
	},
	KeyAWSElastiCache: {{
		From: `module.aws_cloudwatchmonitoring.aws_cloudwatch_metric_alarm.redis_cpu_high["0"]`,
		To:   `module.aws_elasticache.aws_cloudwatch_metric_alarm.cpu_high["0"]`,
	}},
	KeyAWSSQS: {{
		From: `module.aws_cloudwatchmonitoring.aws_cloudwatch_metric_alarm.sqs_backlog["0"]`,
		To:   `module.aws_sqs.aws_cloudwatch_metric_alarm.backlog["0"]`,
	}},
}

// ObservabilityMoves returns the moved {} entries the composer should
// emit alongside the per-component module for ComponentKey k. Returns
// nil if k has no observability move history.
func ObservabilityMoves(k ComponentKey) []MovedRef {
	src := observabilityMoves[k]
	if len(src) == 0 {
		return nil
	}
	out := make([]MovedRef, len(src))
	copy(out, src)
	return out
}
