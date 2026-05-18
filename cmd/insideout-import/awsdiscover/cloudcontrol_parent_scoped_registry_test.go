package awsdiscover

import "testing"

// parentScopedCFNTypes is the curated registry of CloudFormation types whose
// Cloud Control ListResources handler requires a non-empty ResourceModel
// (e.g. {"ClusterName":"..."}). Every entry MUST be wired with ParentLister
// in cloudControlTypeConfigs, and every entry in cloudControlTypeConfigs
// with ParentLister != nil MUST appear here. Drift is a release-blocking
// fail in TestCloudControlTypes_ParentScopedRegistryMatchesTable (#616).
//
// To add a new parent-scoped type:
//  1. Add the CFN type string here.
//  2. Add the matching entry in cloudcontrol_types.go with
//     ParentLister: <listerFn>.
//
// The integration test TestLive616_FullScanNoInvalidRequest is the live
// backstop that catches types we forgot to register here (it walks
// SupportedTypes against a real account and fails any
// InvalidRequestException from CC ListResources).
var parentScopedCFNTypes = map[string]struct{}{
	"AWS::ApiGateway::Deployment":            {},
	"AWS::ApiGateway::Resource":              {},
	"AWS::ApiGateway::Stage":                 {},
	"AWS::ApiGatewayV2::Authorizer":          {},
	"AWS::ApiGatewayV2::Integration":         {},
	"AWS::ApiGatewayV2::Route":               {},
	"AWS::Cognito::UserPoolClient":           {},
	"AWS::Cognito::UserPoolIdentityProvider": {},
	"AWS::Cognito::UserPoolResourceServer":   {},
	"AWS::EKS::AccessEntry":                  {},
	"AWS::EKS::Addon":                        {},
	"AWS::EKS::FargateProfile":               {},
	"AWS::EKS::Nodegroup":                    {},
	"AWS::EKS::PodIdentityAssociation":       {}, // #616
	"AWS::ElasticLoadBalancingV2::Listener":  {}, // #616 follow-up
	"AWS::Lambda::Alias":                     {},
	"AWS::Lambda::Permission":                {},
	"AWS::Lambda::Url":                       {},
	"AWS::Logs::LogStream":                   {},
	"AWS::WAFv2::WebACL":                     {},
}

// TestCloudControlTypes_ParentScopedRegistryMatchesTable enforces the
// bidirectional invariant between parentScopedCFNTypes (the curated
// registry of "this CFN type requires a ResourceModel for CC
// ListResources") and the production cloudControlTypeConfigs table.
//
// Direction 1 — every registry entry must be wired with ParentLister in
// the table. A missing wiring causes the discoverer to call CC
// ListResources with no ResourceModel and AWS rejects with
// InvalidRequestException (HTTP 400) — exactly the #616 failure.
//
// Direction 2 — every table entry that sets ParentLister must appear in
// the registry. Catches accidental removal from the registry that would
// silently weaken Direction 1.
func TestCloudControlTypes_ParentScopedRegistryMatchesTable(t *testing.T) {
	t.Parallel()

	tableByCFN := make(map[string]cloudControlConfig, len(cloudControlTypeConfigs))
	for _, cfg := range cloudControlTypeConfigs {
		tableByCFN[cfg.CloudFormationType] = cfg
	}

	for cfnType := range parentScopedCFNTypes {
		cfg, ok := tableByCFN[cfnType]
		if !ok {
			t.Errorf("registry references CFN type %q but cloudControlTypeConfigs has no entry for it — remove from registry or add the entry", cfnType)
			continue
		}
		if cfg.ParentLister == nil {
			t.Errorf("registry says %q is parent-scoped but cloudControlTypeConfigs entry (TFType=%s) has ParentLister == nil — CC ListResources will reject with HTTP 400 on RGT cache miss", cfnType, cfg.TFType)
		}
	}

	for _, cfg := range cloudControlTypeConfigs {
		if cfg.ParentLister == nil {
			continue
		}
		if _, ok := parentScopedCFNTypes[cfg.CloudFormationType]; !ok {
			t.Errorf("cloudControlTypeConfigs entry %q (TFType=%s) sets ParentLister but is missing from parentScopedCFNTypes — add it to the registry so the bidirectional invariant holds", cfg.CloudFormationType, cfg.TFType)
		}
	}
}
