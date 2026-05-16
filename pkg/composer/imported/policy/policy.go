package policy

// FieldPolicy is the per-attribute Layer 2 decision for an imported
// resource. See docs/managed-resource-tiers.md "Layer 2 — hand-curated
// field policy map". Six axes vary independently; each has its own typed
// string with its own Valid() — the lint runs Valid() on each before
// checking cross-axis rules.
//
// Rationale is a human-readable note used by the lint to gate
// visible-and-Sensitive entries. Most policies leave it empty.
//
// LabelDriftIgnorePrefixes is consulted only when DriftSemantic ==
// DriftSemanticLabelFilter. Each entry is a string-prefix match against
// the per-key name in a map[string]any-shaped attribute; matching keys
// are dropped on both sides before the per-key delta is computed. Empty
// (the zero value) falls back to a built-in GCP-noise default of
// {"goog-", "goog_"} so existing policies that pre-date this knob keep
// the historical behavior.
//
// TagDriftIgnorePrefixes is the AWS-side parallel to
// LabelDriftIgnorePrefixes — same shape, same semantics, separate
// field so each cloud's helper (gcpLabelDriftPolicy /
// awsTagDriftPolicy) reads naturally at the call site. The comparator
// in pkg/drift/imported unions both lists before filtering keys, so a
// curator who wants both Google and AWS prefixes filtered on a
// cross-cloud synthetic policy can populate both fields. Empty
// (the zero value) contributes nothing to the filter set.
type FieldPolicy struct {
	Role                     FieldRole
	Pillar                   FieldPillar
	Visibility               VisibilityPolicy
	Edit                     EditPolicy
	Sensitivity              SensitivityPolicy
	ChangeRisk               ChangeRiskPolicy
	DriftSemantic            DriftSemantic
	LabelDriftIgnorePrefixes []string
	TagDriftIgnorePrefixes   []string
	Rationale                string
}

// Map is a curated field policy keyed by Terraform attribute path. See
// path.go for the path grammar.
type Map map[string]FieldPolicy

// tagPolicy is the uniform treatment for tag- and label-shaped
// attributes (tags, tags_all, labels, effective_labels,
// terraform_labels, annotations, effective_annotations). The interactive
// agent never authors these; the importer / composer system writes them
// and the diff layer redacts user-set values during display. Used by
// every curated map.
func tagPolicy() FieldPolicy {
	return FieldPolicy{
		Role:        RoleTuning,
		Visibility:  VisibilityHidden,
		Edit:        EditSystemOnly,
		Sensitivity: SensitivityRedacted,
	}
}

// timeoutsPolicy is the uniform treatment for the singleton timeouts
// block present on most resources (create / update / delete / read).
// Treated as system-owned operational metadata, hidden from the interactive agent.
func timeoutsPolicy() FieldPolicy {
	return FieldPolicy{
		Role:       RoleTuning,
		Visibility: VisibilityHidden,
		Edit:       EditSystemOnly,
	}
}

// gcpLabelDriftIgnorePrefixes is the canonical set of label-key
// prefixes that must be filtered out of GCP label drift on both
// snapshot and live sides before per-key comparison:
//
//   - "goog-" / "goog_" — GCP control-plane auto-populated labels
//     (e.g. goog-managed-by, goog_terraform_provisioned). A Google
//     SRE bumping any of these would otherwise emit drift on every
//     bucket / topic / secret in the inventory.
//   - "insideout-import" — the importer / composer's own provenance
//     labels (insideout-imported, insideout-imported-at,
//     insideout-import-project, insideout-import-session). The four
//     reserved keys all share the prefix without a trailing hyphen,
//     so one short prefix covers them all. Bumping these on
//     re-emission is expected behavior, not drift.
//
// Used by gcpLabelDriftPolicy() and any per-type policy that opts into
// curated label drift. Mirrors reliable's importedDriftLabelPrefixIgnore
// (internal/agentapi/imported_drift.go) so the per-key drift surface is
// identical between the upstream comparator and the legacy comparator
// it replaces (#1479 Surface B).
var gcpLabelDriftIgnorePrefixes = []string{
	"goog-",
	"goog_",
	"insideout-import",
}

// gcpLabelDriftPolicy is the uniform treatment for GCP `labels`-shaped
// map attributes that we want curated drift on. Same axes as
// tagPolicy() — system-owned, redacted from display — but
// DriftSemantic=LabelFilter with the canonical GCP ignore-prefix set
// so the comparator emits one per-key `labels.<key>` mismatch for each
// user-curated label that diverges between snapshot and live.
//
// Use this in lieu of tagPolicy() on any GCP type whose user-set
// labels are part of the drift signal. Adopting gcpLabelDriftPolicy()
// is policy-author opt-in rather than a global flip so types with no
// useful label-drift surface (or types whose label drift is noise) can
// stay on tagPolicy().
func gcpLabelDriftPolicy() FieldPolicy {
	return FieldPolicy{
		Role:                     RoleTuning,
		Visibility:               VisibilityHidden,
		Edit:                     EditSystemOnly,
		Sensitivity:              SensitivityRedacted,
		DriftSemantic:            DriftSemanticLabelFilter,
		LabelDriftIgnorePrefixes: gcpLabelDriftIgnorePrefixes,
	}
}

// awsTagDriftIgnorePrefixes is the canonical set of AWS tag-key
// prefixes that must be filtered out of AWS tag drift on both
// snapshot and live sides before per-key comparison:
//
//   - "aws:" — the reserved AWS-system prefix
//     (https://docs.aws.amazon.com/tag-editor/latest/userguide/tagging.html).
//     AWS-internal services write keys like
//     `aws:cloudformation:stack-name`, `aws:autoscaling:groupName`,
//     `aws:ec2spot:fleet-request-id`. Terraform does not own these and
//     the AWS API rejects attempts to set them — drift on this prefix
//     is always noise.
//   - "eks:" — EKS-managed tags propagated onto cluster-owned
//     resources (e.g. `eks:cluster-name`, `eks:nodegroup-name`).
//     The EKS control plane writes these on managed node groups,
//     fargate profiles, launch templates, and downstream ASGs; they
//     reappear after every reconcile loop.
//   - "elasticbeanstalk:" — Elastic Beanstalk environment tags
//     (e.g. `elasticbeanstalk:environment-name`,
//     `elasticbeanstalk:environment-id`) auto-applied to every
//     resource a Beanstalk environment provisions.
//   - "kubernetes.io/" — the Kubernetes-on-AWS convention used by
//     in-tree cloud providers and AWS Load Balancer Controller to
//     mark cluster-owned EBS volumes, ENIs, security groups, target
//     groups, and subnets (e.g. `kubernetes.io/cluster/<name>`,
//     `kubernetes.io/role/elb`). The controller re-applies them on
//     every reconcile.
//   - "InsideOut" / "insideout-" — the importer / composer's own
//     provenance tags (mirror of gcpLabelDriftIgnorePrefixes's
//     "insideout-import" entry). Both casings are listed because
//     AWS tag keys are case-sensitive and historical exports have
//     used mixed casing.
//
// Used by awsTagDriftPolicy() and any per-type policy that opts into
// curated tag drift. Symmetric with gcpLabelDriftIgnorePrefixes so the
// drift comparator's filter behavior is policy-author authored rather
// than baked into compare.go.
var awsTagDriftIgnorePrefixes = []string{
	"aws:",
	"eks:",
	"elasticbeanstalk:",
	"kubernetes.io/",
	"InsideOut",
	"insideout-",
}

// awsTagDriftPolicy is the uniform treatment for AWS `tags` / `tags_all`
// map attributes that we want curated drift on. Same axes as
// tagPolicy() — system-owned, redacted from display — but
// DriftSemantic=LabelFilter with the canonical AWS-managed prefix set
// so the comparator emits one per-key `tags.<key>` mismatch for each
// user-curated tag that diverges between snapshot and live.
//
// Use this in lieu of tagPolicy() on any AWS type whose user-set
// tags are part of the drift signal — most notably the canonical
// `Project = <project>` tag that the downstream InsideOut inspector
// uses to attribute resources (CLAUDE.md "Project tag is required
// on every taggable AWS resource"). Out-of-band tag removal is
// invisible to drift detection without this; an operator stripping
// `Project` silently breaks attribution (#81 backstory, #568).
//
// Adopting awsTagDriftPolicy() is policy-author opt-in rather than a
// global flip so types with no useful tag-drift surface can stay on
// tagPolicy() and types like aws_autoscaling_group whose tags churn
// from EKS / Karpenter / ASG-internal writes don't generate noise.
//
// Mirrors gcpLabelDriftPolicy() — same shape, AWS-flavored prefix
// set, system-owned semantics. See awsTagDriftIgnorePrefixes for the
// rationale on each filter prefix.
func awsTagDriftPolicy() FieldPolicy {
	return FieldPolicy{
		Role:                   RoleTuning,
		Visibility:             VisibilityHidden,
		Edit:                   EditSystemOnly,
		Sensitivity:            SensitivityRedacted,
		DriftSemantic:          DriftSemanticLabelFilter,
		TagDriftIgnorePrefixes: awsTagDriftIgnorePrefixes,
	}
}
