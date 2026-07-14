package imported

// discover_projection.go — per-type "discover summary" and label projection
// for the reverse-Terraform import wizard.
//
// DiscoverSummary / DiscoverLabels are the presets-owned home for the curated
// one-line attribute summary and the enriched-label overlay the importer
// wizard renders beneath each discovered resource. They moved here from
// reliable's internal/agentapi/import_dtos.go (reliable#2239, umbrella #1479)
// so the per-type vocabulary lives upstream — reliable holds zero per-type
// dispatch for the DTO summary/label surface.
//
// Contract (mirrors the reliable mappers they replace, byte-for-byte):
//
//   - DiscoverSummary returns the curated one-line summary for the five GCP
//     types that carry one, or "" for every other type (an unmapped type
//     rendered a blank summary before, and still does). Summaries never fail:
//     Attrs that fail to decode collapse to a shorter summary (or "") rather
//     than panicking, so the wizard renders a partial row instead of dropping
//     it.
//   - DiscoverLabels returns the enriched user-label overlay (a flat
//     string→string map) for the types whose summary mapper surfaced labels,
//     or an empty (non-nil) map otherwise. The consumer overlays these onto
//     the base identity tags.
//
// The typed attr views mirror the relevant subset of the generated per-type
// models (generated.GoogleStorageBucket, …) and Value[T]'s JSON wire shape
// ({"literal": …}); the payload is decoded from the raw ImportedResource.Attrs
// JSON so this file stays free of any dependency on the generated package
// (same rationale as resource.go's Attrs=json.RawMessage note).

import (
	"encoding/json"
	"strconv"
	"strings"
)

// DiscoverSummary returns the importer wizard's one-line attribute summary for
// a discovered resource, dispatching on Identity.Type. Returns "" for types
// without a curated summary (matching the pre-move identity-only DTO).
func DiscoverSummary(ir ImportedResource) string {
	switch ir.Identity.Type {
	case "google_storage_bucket":
		return buildBucketSummary(ir)
	case "google_pubsub_topic":
		return buildPubsubTopicSummary(ir)
	case "google_pubsub_subscription":
		return buildPubsubSubscriptionSummary(ir)
	case "google_secret_manager_secret":
		return buildSecretManagerSecretSummary(ir)
	case "google_compute_network":
		return buildComputeNetworkSummary(ir)
	default:
		return ""
	}
}

// DiscoverLabels returns the enriched user-label overlay for a discovered
// resource, dispatching on Identity.Type. Returns an empty (non-nil) map for
// types with no label surface (including google_compute_network, whose
// generated schema exposes none). The map is the type-specific overlay only —
// the consumer merges it onto the base identity tags.
func DiscoverLabels(ir ImportedResource) map[string]string {
	switch ir.Identity.Type {
	case "google_storage_bucket":
		return readBucketLabels(ir)
	case "google_pubsub_topic":
		return readPubsubTopicLabels(ir)
	case "google_pubsub_subscription":
		return readPubsubSubscriptionLabels(ir)
	case "google_secret_manager_secret":
		return readSecretManagerSecretLabels(ir)
	default:
		return map[string]string{}
	}
}

// discoverLastPathSegment returns the substring after the last "/" of s, or
// the empty string when s has no slash or is empty. Used to render the short
// name of a fully-qualified Pub/Sub topic path (e.g. "projects/p/topics/t" →
// "t").
func discoverLastPathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

/* ------------------------------------------------------------------ */
/* google_storage_bucket                                              */
/* ------------------------------------------------------------------ */

// bucketAttrsView is the minimal typed projection of ir.Attrs needed to build
// the wizard's DTO summary + tags. Mirrors the relevant subset of
// generated.GoogleStorageBucket (storage_class, labels) and Value[T]'s JSON
// wire shape ({"literal": …}).
type bucketAttrsView struct {
	StorageClass *struct {
		Literal string `json:"literal"`
	} `json:"StorageClass,omitempty"`
	Labels map[string]struct {
		Literal string `json:"literal"`
	} `json:"Labels,omitempty"`
}

// buildBucketSummary returns "<storage_class> · <location>". Falls back to just
// location when Attrs are absent / unparseable.
func buildBucketSummary(ir ImportedResource) string {
	parts := []string{}
	if v, ok := readBucketAttrsView(ir); ok && v.StorageClass != nil && v.StorageClass.Literal != "" {
		parts = append(parts, v.StorageClass.Literal)
	}
	if ir.Identity.Location != "" {
		parts = append(parts, ir.Identity.Location)
	}
	return strings.Join(parts, " · ")
}

// readBucketLabels surfaces labels from the enriched Attrs as a flat
// string→string map. Empty map (not nil) when no labels are populated.
func readBucketLabels(ir ImportedResource) map[string]string {
	out := map[string]string{}
	v, ok := readBucketAttrsView(ir)
	if !ok || v.Labels == nil {
		return out
	}
	for k, lit := range v.Labels {
		out[k] = lit.Literal
	}
	return out
}

// readBucketAttrsView parses ir.Attrs into the minimal bucketAttrsView.
// Returns (zero, false) on absent / unparseable Attrs.
func readBucketAttrsView(ir ImportedResource) (bucketAttrsView, bool) {
	if len(ir.Attrs) == 0 {
		return bucketAttrsView{}, false
	}
	var v bucketAttrsView
	if err := json.Unmarshal(ir.Attrs, &v); err != nil {
		return bucketAttrsView{}, false
	}
	return v, true
}

/* ------------------------------------------------------------------ */
/* google_pubsub_topic                                                */
/* ------------------------------------------------------------------ */

// pubsubTopicAttrsView is the minimal typed projection of ir.Attrs needed to
// build the wizard's DTO summary + tags. Field names mirror
// generated.GooglePubsubTopic; the nested MessageStoragePolicy is decoded as a
// list since the generated type declares it as a TF repeated-block.
type pubsubTopicAttrsView struct {
	MessageRetentionDuration *struct {
		Literal string `json:"literal"`
	} `json:"MessageRetentionDuration,omitempty"`
	Labels map[string]struct {
		Literal string `json:"literal"`
	} `json:"Labels,omitempty"`
	MessageStoragePolicy []struct {
		AllowedPersistenceRegions []*struct {
			Literal string `json:"literal"`
		} `json:"AllowedPersistenceRegions,omitempty"`
	} `json:"MessageStoragePolicy,omitempty"`
}

// buildPubsubTopicSummary returns "<regions> · <retention>". Empty parts are
// skipped; both absent collapses to "".
func buildPubsubTopicSummary(ir ImportedResource) string {
	v, ok := readPubsubTopicAttrsView(ir)
	if !ok {
		return ""
	}
	parts := []string{}
	regions := pubsubAllowedRegions(v)
	if len(regions) > 0 {
		parts = append(parts, strings.Join(regions, ","))
	}
	if v.MessageRetentionDuration != nil && v.MessageRetentionDuration.Literal != "" {
		parts = append(parts, v.MessageRetentionDuration.Literal)
	}
	return strings.Join(parts, " · ")
}

// pubsubAllowedRegions flattens the typed repeated-block representation of
// message_storage_policy.allowed_persistence_regions into a flat string slice.
// Non-literal entries are dropped. Returns an empty slice (not nil).
func pubsubAllowedRegions(v pubsubTopicAttrsView) []string {
	if len(v.MessageStoragePolicy) == 0 {
		return []string{}
	}
	regions := make([]string, 0)
	for _, block := range v.MessageStoragePolicy {
		for _, r := range block.AllowedPersistenceRegions {
			if r == nil || r.Literal == "" {
				continue
			}
			regions = append(regions, r.Literal)
		}
	}
	return regions
}

// readPubsubTopicLabels surfaces labels from the enriched Attrs as a flat
// string→string map. Empty map (not nil) when no labels are populated.
func readPubsubTopicLabels(ir ImportedResource) map[string]string {
	out := map[string]string{}
	v, ok := readPubsubTopicAttrsView(ir)
	if !ok || v.Labels == nil {
		return out
	}
	for k, lit := range v.Labels {
		out[k] = lit.Literal
	}
	return out
}

// readPubsubTopicAttrsView parses ir.Attrs into the minimal
// pubsubTopicAttrsView. Returns (zero, false) on absent / unparseable Attrs.
func readPubsubTopicAttrsView(ir ImportedResource) (pubsubTopicAttrsView, bool) {
	if len(ir.Attrs) == 0 {
		return pubsubTopicAttrsView{}, false
	}
	var v pubsubTopicAttrsView
	if err := json.Unmarshal(ir.Attrs, &v); err != nil {
		return pubsubTopicAttrsView{}, false
	}
	return v, true
}

/* ------------------------------------------------------------------ */
/* google_pubsub_subscription                                         */
/* ------------------------------------------------------------------ */

// pubsubSubscriptionAttrsView is the minimal typed projection of ir.Attrs
// needed to build the wizard's DTO summary + tags. Mirrors
// generated.GooglePubsubSubscription.
type pubsubSubscriptionAttrsView struct {
	Topic *struct {
		Literal string `json:"literal"`
	} `json:"Topic,omitempty"`
	AckDeadlineSeconds *struct {
		Literal int64 `json:"literal"`
	} `json:"AckDeadlineSeconds,omitempty"`
	Labels map[string]struct {
		Literal string `json:"literal"`
	} `json:"Labels,omitempty"`
}

// buildPubsubSubscriptionSummary renders "topic=<short-topic> · <ack>s". Topic
// is rendered as its short last-path-segment so the summary stays scannable.
func buildPubsubSubscriptionSummary(ir ImportedResource) string {
	v, ok := readPubsubSubscriptionAttrsView(ir)
	if !ok {
		return ""
	}
	parts := []string{}
	if v.Topic != nil && v.Topic.Literal != "" {
		topicShort := discoverLastPathSegment(v.Topic.Literal)
		if topicShort == "" {
			topicShort = v.Topic.Literal
		}
		parts = append(parts, "topic="+topicShort)
	}
	if v.AckDeadlineSeconds != nil && v.AckDeadlineSeconds.Literal > 0 {
		parts = append(parts, strconv.FormatInt(v.AckDeadlineSeconds.Literal, 10)+"s")
	}
	return strings.Join(parts, " · ")
}

// readPubsubSubscriptionLabels surfaces user labels from ir.Attrs as a flat
// string→string map. Empty map (not nil) when no labels populated.
func readPubsubSubscriptionLabels(ir ImportedResource) map[string]string {
	out := map[string]string{}
	v, ok := readPubsubSubscriptionAttrsView(ir)
	if !ok || v.Labels == nil {
		return out
	}
	for k, lit := range v.Labels {
		out[k] = lit.Literal
	}
	return out
}

// readPubsubSubscriptionAttrsView parses ir.Attrs into the minimal
// pubsubSubscriptionAttrsView. Returns (zero, false) on absent / unparseable
// Attrs.
func readPubsubSubscriptionAttrsView(ir ImportedResource) (pubsubSubscriptionAttrsView, bool) {
	if len(ir.Attrs) == 0 {
		return pubsubSubscriptionAttrsView{}, false
	}
	var v pubsubSubscriptionAttrsView
	if err := json.Unmarshal(ir.Attrs, &v); err != nil {
		return pubsubSubscriptionAttrsView{}, false
	}
	return v, true
}

/* ------------------------------------------------------------------ */
/* google_secret_manager_secret                                       */
/* ------------------------------------------------------------------ */

// secretManagerSecretAttrsView is the minimal typed projection of ir.Attrs
// needed to build the wizard's DTO summary + tags. Mirrors
// generated.GoogleSecretManagerSecret. Replication is a repeated block with
// mutually-exclusive Auto/UserManaged sub-blocks; the summary uses whichever
// is present.
type secretManagerSecretAttrsView struct {
	Replication []struct {
		Auto []struct {
			CustomerManagedEncryption []json.RawMessage `json:"CustomerManagedEncryption,omitempty"`
		} `json:"Auto,omitempty"`
		UserManaged []struct {
			Replicas []struct {
				Location *struct {
					Literal string `json:"literal"`
				} `json:"Location,omitempty"`
			} `json:"Replicas,omitempty"`
		} `json:"UserManaged,omitempty"`
	} `json:"Replication,omitempty"`
	Rotation []struct {
		RotationPeriod *struct {
			Literal string `json:"literal"`
		} `json:"RotationPeriod,omitempty"`
	} `json:"Rotation,omitempty"`
	Labels map[string]struct {
		Literal string `json:"literal"`
	} `json:"Labels,omitempty"`
}

// buildSecretManagerSecretSummary renders "<replication> · rotate=<period>".
// Replication is "auto" / "auto+cmek" / "user-managed:r1,r2".
func buildSecretManagerSecretSummary(ir ImportedResource) string {
	v, ok := readSecretManagerSecretAttrsView(ir)
	if !ok {
		return ""
	}
	parts := []string{}
	if rep := secretManagerReplicationSummary(v); rep != "" {
		parts = append(parts, rep)
	}
	if len(v.Rotation) > 0 {
		r := v.Rotation[0]
		if r.RotationPeriod != nil && r.RotationPeriod.Literal != "" {
			parts = append(parts, "rotate="+r.RotationPeriod.Literal)
		}
	}
	return strings.Join(parts, " · ")
}

// secretManagerReplicationSummary returns a short human-readable description of
// the replication policy: "auto", "auto+cmek", or "user-managed:r1,r2". Empty
// when no replication block is populated.
func secretManagerReplicationSummary(v secretManagerSecretAttrsView) string {
	if len(v.Replication) == 0 {
		return ""
	}
	rep := v.Replication[0]
	if len(rep.Auto) > 0 {
		if len(rep.Auto[0].CustomerManagedEncryption) > 0 {
			return "auto+cmek"
		}
		return "auto"
	}
	if len(rep.UserManaged) > 0 {
		um := rep.UserManaged[0]
		locs := make([]string, 0, len(um.Replicas))
		for _, r := range um.Replicas {
			if r.Location != nil && r.Location.Literal != "" {
				locs = append(locs, r.Location.Literal)
			}
		}
		if len(locs) == 0 {
			return "user-managed"
		}
		return "user-managed:" + strings.Join(locs, ",")
	}
	return ""
}

// readSecretManagerSecretLabels surfaces user labels from ir.Attrs. Empty map
// (not nil) when no labels populated.
func readSecretManagerSecretLabels(ir ImportedResource) map[string]string {
	out := map[string]string{}
	v, ok := readSecretManagerSecretAttrsView(ir)
	if !ok || v.Labels == nil {
		return out
	}
	for k, lit := range v.Labels {
		out[k] = lit.Literal
	}
	return out
}

// readSecretManagerSecretAttrsView parses ir.Attrs into the minimal
// secretManagerSecretAttrsView. Returns (zero, false) on absent / unparseable
// Attrs.
func readSecretManagerSecretAttrsView(ir ImportedResource) (secretManagerSecretAttrsView, bool) {
	if len(ir.Attrs) == 0 {
		return secretManagerSecretAttrsView{}, false
	}
	var v secretManagerSecretAttrsView
	if err := json.Unmarshal(ir.Attrs, &v); err != nil {
		return secretManagerSecretAttrsView{}, false
	}
	return v, true
}

/* ------------------------------------------------------------------ */
/* google_compute_network                                             */
/* ------------------------------------------------------------------ */

// computeNetworkAttrsView is the minimal typed projection of ir.Attrs needed
// for the summary. Mirrors generated.GoogleComputeNetwork field names.
type computeNetworkAttrsView struct {
	RoutingMode *struct {
		Literal string `json:"literal"`
	} `json:"RoutingMode,omitempty"`
	AutoCreateSubnetworks *struct {
		Literal bool `json:"literal"`
	} `json:"AutoCreateSubnetworks,omitempty"`
}

// buildComputeNetworkSummary renders "routing=<mode> · auto_subnets=<bool>".
func buildComputeNetworkSummary(ir ImportedResource) string {
	v, ok := readComputeNetworkAttrsView(ir)
	if !ok {
		return ""
	}
	parts := []string{}
	if v.RoutingMode != nil && v.RoutingMode.Literal != "" {
		parts = append(parts, "routing="+v.RoutingMode.Literal)
	}
	if v.AutoCreateSubnetworks != nil {
		parts = append(parts, "auto_subnets="+strconv.FormatBool(v.AutoCreateSubnetworks.Literal))
	}
	return strings.Join(parts, " · ")
}

// readComputeNetworkAttrsView parses ir.Attrs into the minimal
// computeNetworkAttrsView. Returns (zero, false) on absent / unparseable
// Attrs.
func readComputeNetworkAttrsView(ir ImportedResource) (computeNetworkAttrsView, bool) {
	if len(ir.Attrs) == 0 {
		return computeNetworkAttrsView{}, false
	}
	var v computeNetworkAttrsView
	if err := json.Unmarshal(ir.Attrs, &v); err != nil {
		return computeNetworkAttrsView{}, false
	}
	return v, true
}
