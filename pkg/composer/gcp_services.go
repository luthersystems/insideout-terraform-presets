package composer

import "sort"

// GCPService describes a single GCP service (a.k.a. API) that must be
// enabled on the target project for a Luther terraform module to apply
// cleanly. Name is what serviceusage.batchGet expects; Title is the
// human-readable form rendered in ui-core's "missing APIs" banner.
type GCPService struct {
	// Name is the service identifier, e.g. "compute.googleapis.com".
	Name string
	// Title is the display label, e.g. "Compute Engine".
	Title string
}

// AlwaysRequiredGCPServices are the services every Luther GCP deploy needs,
// regardless of which composer components the caller selects:
//   - Service Usage itself (required for the batchGet pre-flight to work).
//   - Cloud Resource Manager (terraform reads project metadata).
//   - IAM (every stack mints a service account or IAM binding).
//   - Compute (we always create at least a VPC).
//
// Source of truth ui-core consumes (issue #195). Values are copied verbatim
// from ui-core's prior alwaysRequiredGCPAPIs.
var AlwaysRequiredGCPServices = []GCPService{
	{Name: "serviceusage.googleapis.com", Title: "Service Usage"},
	{Name: "cloudresourcemanager.googleapis.com", Title: "Cloud Resource Manager"},
	{Name: "iam.googleapis.com", Title: "Identity and Access Management"},
	{Name: "compute.googleapis.com", Title: "Compute Engine"},
}

// GCPServices maps composer ComponentKey to the GCP services the target
// project must have enabled to apply that component's terraform module.
// Values are the service names accepted by serviceusage.batchGet plus a
// human-readable Title for the UI surface that lists missing APIs.
//
// Source of truth for ui-core's prior gcpComponentToAPIs (issue #195).
// Every GCP-backed key in AllComponentKeys must have an entry here; the
// drift-guard test (TestGCPServices_CoverAllGCPKeys) fails the package
// build otherwise. nil values are permitted and used for components
// already covered by AlwaysRequiredGCPServices (e.g. the load balancer is
// covered by Compute Engine).
var GCPServices = map[ComponentKey][]GCPService{
	KeyGCPCloudKMS: {{Name: "cloudkms.googleapis.com", Title: "Cloud KMS"}},
	KeyGCPCloudSQL: {{Name: "sqladmin.googleapis.com", Title: "Cloud SQL Admin"}},
	KeyGCPGKE:      {{Name: "container.googleapis.com", Title: "Kubernetes Engine"}},
	KeyGCPGCS:      {{Name: "storage.googleapis.com", Title: "Cloud Storage"}},
	KeyGCPCloudRun: {{Name: "run.googleapis.com", Title: "Cloud Run"}},
	KeyGCPCloudFunctions: {
		{Name: "cloudfunctions.googleapis.com", Title: "Cloud Functions"},
		// Cloud Functions Gen 2 builds container images via Cloud Build.
		{Name: "cloudbuild.googleapis.com", Title: "Cloud Build"},
		// Serverless VPC Access connectors are required when functions
		// egress into a VPC.
		{Name: "vpcaccess.googleapis.com", Title: "Serverless VPC Access"},
	},
	KeyGCPPubSub:           {{Name: "pubsub.googleapis.com", Title: "Pub/Sub"}},
	KeyGCPMemorystore:      {{Name: "redis.googleapis.com", Title: "Memorystore for Redis"}},
	KeyGCPSecretManager:    {{Name: "secretmanager.googleapis.com", Title: "Secret Manager"}},
	KeyGCPCloudLogging:     {{Name: "logging.googleapis.com", Title: "Cloud Logging"}},
	KeyGCPCloudMonitoring:  {{Name: "monitoring.googleapis.com", Title: "Cloud Monitoring"}},
	KeyGCPIdentityPlatform: {{Name: "identitytoolkit.googleapis.com", Title: "Identity Toolkit"}},
	KeyGCPCloudBuild:       {{Name: "cloudbuild.googleapis.com", Title: "Cloud Build"}},
	KeyGCPFirestore:        {{Name: "firestore.googleapis.com", Title: "Cloud Firestore"}},
	KeyGCPVertexAI:         {{Name: "aiplatform.googleapis.com", Title: "Vertex AI"}},
	KeyGCPAPIGateway: {
		{Name: "apigateway.googleapis.com", Title: "API Gateway"},
		// API Gateway plane requires both Service Control (request
		// gating) and Service Management (managed-service config push).
		// Without these enabled, terraform fails on the first
		// managed-service create.
		{Name: "servicecontrol.googleapis.com", Title: "Service Control"},
		{Name: "servicemanagement.googleapis.com", Title: "Service Management"},
	},
	KeyGCPBackups:  {{Name: "backupdr.googleapis.com", Title: "Backup and DR Service"}},
	KeyGCPCloudDNS: {{Name: "dns.googleapis.com", Title: "Cloud DNS"}},
	// GCP GitHub Actions WIF preset (#597 row 1). The preset enables
	// iam.googleapis.com (covered by always-required) plus IAM Credentials
	// (the STS token-minting endpoint the action calls) and STS itself
	// (the federation endpoint). Without these enabled, terraform apply
	// succeeds but the first GitHub Actions workflow run fails at the
	// auth step with PERMISSION_DENIED.
	KeyGCPGitHubActions: {
		{Name: "iamcredentials.googleapis.com", Title: "IAM Service Account Credentials"},
		{Name: "sts.googleapis.com", Title: "Security Token Service"},
	},
	// GCP Cloud Deploy delivery-pipeline preset (#613). The preset enables
	// clouddeploy.googleapis.com itself via google_project_service; the
	// always-required IAM activation covers the runner SA + role bindings.
	// Without this entry, the pre-deploy serviceusage.batchGet check
	// would silently omit Cloud Deploy and the first
	// google_clouddeploy_delivery_pipeline create fails with a
	// SERVICE_DISABLED 403.
	KeyGCPCloudDeploy: {
		{Name: "clouddeploy.googleapis.com", Title: "Cloud Deploy"},
	},
	// Components that need no extra service beyond the always-required set
	// (covered by Compute / always-required entries):
	KeyGCPVPC:          nil,
	KeyGCPCompute:      nil,
	KeyGCPBastion:      nil,
	KeyGCPLoadbalancer: nil, // covered by always-required Compute Engine.
	KeyGCPCloudArmor:   nil, // covered by always-required Compute Engine.
}

// RequiredGCPServices returns the deduplicated list of GCP services the
// target project must have enabled to deploy the given components,
// including AlwaysRequiredGCPServices. Output is sorted by Name so
// serviceusage.batchGet input is deterministic and test snapshots compare
// cleanly. Unknown component keys are silently ignored (forward-compat: a
// presets release introducing a new component shouldn't break in-flight
// consumers).
func RequiredGCPServices(components []ComponentKey) []GCPService {
	want := len(AlwaysRequiredGCPServices) + 2*len(components)
	seen := make(map[string]bool, want)
	out := make([]GCPService, 0, want)

	add := func(s GCPService) {
		if seen[s.Name] {
			return
		}
		seen[s.Name] = true
		out = append(out, s)
	}

	for _, s := range AlwaysRequiredGCPServices {
		add(s)
	}
	for _, c := range components {
		for _, s := range GCPServices[c] {
			add(s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
