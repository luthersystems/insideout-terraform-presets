// internal/awsdiscover/retry_config.go
//
// Shared AWS SDK v2 retry tuning for every discovery code path.
//
// Discovery makes a large fan-out of CloudControl ListResources / GetResource
// (and native SDK list/describe) calls across many resource types and regions
// that share a per-region API rate budget, so throttling is the dominant
// transient failure mode. The retryer settings that handle it used to live in
// package main (cmd/insideout-import/discover.go) and so were reachable only
// by the CLI — a downstream caller that builds its own aws.Config (notably
// luthersystems/reliable's reverse-import "scan my account" handler) silently
// shipped with the stock SDK retryer (standard mode, 3 attempts) and hit
// throttle aborts the CLI never saw. Centralizing the values here gives every
// caller one source of truth.
package awsdiscover

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

const (
	// RetryMaxAttempts raises the SDK retryer's attempt budget above the v2
	// default of 3 so transient Throttling errors during a multi-thousand-
	// resource discover run don't abort mid-batch. 8 covers the empirical
	// worst case observed in audit data (a saturated ListResources /
	// ListTags fan-out on a few-hundred-resource account); with adaptive
	// backoff (jitter + exponential) attempt 8 lands ~30s after attempt 1.
	RetryMaxAttempts = 8

	// RetryMode pins the SDK retryer to v2's adaptive mode (#632). The
	// default `standard` mode reacts to ThrottlingException after the fact;
	// adaptive mode adds a client-side token bucket that *proactively* slows
	// the send rate when the server signals throttling — the right shape for
	// the parallel DiscoverTypes walk, where per-service goroutines share the
	// same per-region CloudControl rate budget, so a throttle signal from one
	// goroutine should slow the others' first calls too.
	RetryMode = aws.RetryModeAdaptive
)

// RetryLoadOptions returns the config.LoadDefaultConfig options that enable
// the shared adaptive retryer. Use when building an aws.Config via
// config.LoadDefaultConfig:
//
//	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
//	opts = append(opts, awsdiscover.RetryLoadOptions()...)
//	cfg, err := config.LoadDefaultConfig(ctx, opts...)
func RetryLoadOptions() []func(*config.LoadOptions) error {
	return []func(*config.LoadOptions) error{
		config.WithRetryMaxAttempts(RetryMaxAttempts),
		config.WithRetryMode(RetryMode),
	}
}

// ApplyRetryDefaults stamps the shared adaptive retryer onto an
// already-constructed aws.Config — one built as a struct literal rather than
// via config.LoadDefaultConfig (e.g. reliable's role-ARN vended-credentials
// path, which assembles aws.Config{Region, Credentials} by hand). The SDK
// clients built from this config (cloudcontrol.NewFromConfig, …) resolve
// their retryer from cfg.RetryMode + cfg.RetryMaxAttempts when cfg.Retryer is
// unset, so setting these two fields is sufficient. Mutates cfg in place.
func ApplyRetryDefaults(cfg *aws.Config) {
	cfg.RetryMode = RetryMode
	cfg.RetryMaxAttempts = RetryMaxAttempts
}
