// Package labels is the registry of human-friendly display labels and
// icon keys for imported Terraform resource types. It is the upstream
// half of luthersystems/reliable's serviceMeta.ts surface — the
// downstream consumer fetches this via the eventual codegen
// `imported-codegen labels` subcommand (presets#482) and pins UI strings
// to the registry instead of hand-maintaining a parallel map.
//
// Two-step lookup:
//
//  1. The override map (Register) wins. Per-type files in the per-cloud
//     packages add entries on init() when the default rule produces a
//     worse label than the curated one — e.g. "Queue (SQS)" over the
//     default "Sqs Queue".
//  2. Otherwise the default rule strips the cloud prefix and humanizes
//     the remainder ("aws_s3_bucket" → "S3 Bucket", "google_pubsub_topic"
//     → "Pubsub Topic"). Same for icon keys minus the title-casing.
//
// The registry starts empty in this skeleton (presets#TBD). Per-type
// PRs in the enricher rollout populate entries opportunistically.
package labels

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	regMu    sync.RWMutex
	registry = map[string]entry{}
)

type entry struct {
	Label   string
	IconKey string
}

// Register pins an override (label, iconKey) pair for tfType. Either
// or both of label/iconKey may be empty, in which case the default
// rule fills the empty side at lookup time. Panics on duplicate
// registration for the same tfType (mirrors the policy package's
// Register contract — a duplicate means two files compete for the
// same key, which is always a bug).
func Register(tfType, label, iconKey string) {
	if tfType == "" {
		panic("labels.Register: empty tfType")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := registry[tfType]; ok {
		panic(fmt.Sprintf("labels.Register: duplicate registration for %q", tfType))
	}
	registry[tfType] = entry{Label: label, IconKey: iconKey}
}

// Label returns the human-readable display label for tfType. Returns
// the registered override if one is set, else the default rule
// applied to the type name (strip cloud prefix → humanize words).
func Label(tfType string) string {
	regMu.RLock()
	e, ok := registry[tfType]
	regMu.RUnlock()
	if ok && e.Label != "" {
		return e.Label
	}
	return defaultLabel(tfType)
}

// IconKey returns the icon-asset key for tfType. Returns the
// registered override if one is set, else the type name minus the
// cloud prefix ("aws_s3_bucket" → "s3_bucket").
func IconKey(tfType string) string {
	regMu.RLock()
	e, ok := registry[tfType]
	regMu.RUnlock()
	if ok && e.IconKey != "" {
		return e.IconKey
	}
	return defaultIconKey(tfType)
}

// RegisteredTypes returns the sorted set of tfTypes with an override
// entry. Used by the eventual codegen subcommand to enumerate the
// override map for downstream consumers.
func RegisteredTypes() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// defaultLabel produces "S3 Bucket" from "aws_s3_bucket" — strips the
// cloud prefix and title-cases every underscore-delimited word.
func defaultLabel(tfType string) string {
	core := stripCloudPrefix(tfType)
	if core == "" {
		return tfType
	}
	parts := strings.Split(core, "_")
	for i, p := range parts {
		parts[i] = titleCase(p)
	}
	return strings.Join(parts, " ")
}

// defaultIconKey produces "s3_bucket" from "aws_s3_bucket".
func defaultIconKey(tfType string) string {
	core := stripCloudPrefix(tfType)
	if core == "" {
		return tfType
	}
	return core
}

// stripCloudPrefix removes the leading "aws_" or "google_" segment
// from tfType. Returns the input unchanged for inputs that don't
// match a known prefix (the codegen contract is that registered
// types always carry one — defensive return is for unit-test
// edge cases).
func stripCloudPrefix(tfType string) string {
	for _, p := range []string{"aws_", "google_"} {
		if after, ok := strings.CutPrefix(tfType, p); ok {
			return after
		}
	}
	return tfType
}

// titleCase upper-cases the first rune of s and leaves the rest as-is.
// Avoids importing strings.Title (deprecated) and golang.org/x/text/cases
// (an extra module dependency for a one-liner).
func titleCase(s string) string {
	if s == "" {
		return ""
	}
	first := s[0]
	if first >= 'a' && first <= 'z' {
		first -= 'a' - 'A'
	}
	return string(first) + s[1:]
}
