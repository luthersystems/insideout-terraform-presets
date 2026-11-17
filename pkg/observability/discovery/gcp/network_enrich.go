// Enrichment helpers for the network-plane list actions (#928).
//
// The panel-default list calls (list-networks, list-url-maps,
// list-policies) return only what the list endpoint itself carries.
// Some of the config the design-vs-deployed card compares lives on a
// neighbouring resource instead: a network's exposure lives on its
// firewall rules, a URL map's protocol on its backend service, its SSL
// policy on its HTTPS proxy. The helpers here join that data onto the
// list result so the extractors can read it from one inspector call.
//
// Every helper keeps two promises:
//   - A failed extra call never fails the inspection. The caller logs
//     it and the helper leaves the derived field out, so the extractor
//     can tell "not fetched" from "fetched and empty".
//   - Slice-shaped fields marshal as [] never null (CONTRIBUTING.md,
//     #255).

package gcp

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	computeapi "google.golang.org/api/compute/v1"
)

// cloudArmorDescribeCap bounds the describe-policy fan-out in
// list-policies. securityPolicies.list normally returns each policy's
// rules already, so the fan-out only runs for a policy that came back
// without them. A preset stack holds one policy; ten covers that with
// room to spare, while a project with hundreds of hand-made policies
// cannot turn one panel load into hundreds of GETs. Policies past the
// cap are returned as listed.
const cloudArmorDescribeCap = 10

// discoveryToMap round-trips a google.golang.org/api (Discovery) value
// into map[string]any. Those types carry their own MarshalJSON, which
// a wrapper struct cannot extend (embedding promotes the method and
// drops the extra fields), so enrichment works on the map form. The
// keys are the same lowerCamelCase the extractors already read.
func discoveryToMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

// lastPathSegment returns the part of a GCP resource URL after the last
// "/". Self-links and references between Compute resources are full
// URLs in some responses and partial ones in others; resource names are
// unique per project and kind, so the name is the safe join key.
func lastPathSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// listAllFirewalls reads every firewall rule in the project. It is one
// paged call for the whole project, not a call per network, and its
// size is bounded by the project's firewall-rule quota. All pages are
// read because a public rule on page two must still count: stopping
// early would label an exposed network private.
func listAllFirewalls(ctx context.Context, svc *computeapi.Service, projectID string) ([]*computeapi.Firewall, error) {
	out := []*computeapi.Firewall{}
	err := svc.Firewalls.List(projectID).Pages(ctx, func(page *computeapi.FirewallList) error {
		out = append(out, page.Items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// isPublicIngressFirewall reports whether a firewall rule lets traffic
// in from the whole internet: an enabled INGRESS rule with at least one
// allow entry whose source ranges include 0.0.0.0/0 or ::/0. This is the
// GCP counterpart of an AWS VPC with an internet gateway — the signal
// extractVPCConfig uses for deploymentType. Deny rules never expose a
// network, so they do not count.
func isPublicIngressFirewall(fw *computeapi.Firewall) bool {
	if fw == nil || fw.Disabled || len(fw.Allowed) == 0 {
		return false
	}
	// The API defaults an unset direction to INGRESS.
	if fw.Direction != "" && fw.Direction != "INGRESS" {
		return false
	}
	for _, r := range fw.SourceRanges {
		if r == "0.0.0.0/0" || r == "::/0" {
			return true
		}
	}
	return false
}

// networksWithIngress returns the networks as maps. When classified is
// true each map gains publicIngressFirewalls: the names of the
// network's firewall rules that admit traffic from anywhere ([] when
// none, so "private" is explicit). When the firewall list could not be
// read, classified is false and the key is left out, so the extractor
// skips the classification instead of calling every network private.
func networksWithIngress(nets []*computeapi.Network, fws []*computeapi.Firewall, classified bool) []map[string]any {
	public := map[string][]string{}
	for _, fw := range fws {
		if isPublicIngressFirewall(fw) {
			n := lastPathSegment(fw.Network)
			public[n] = append(public[n], fw.Name)
		}
	}
	out := make([]map[string]any, 0, len(nets))
	for _, n := range nets {
		if n == nil {
			continue
		}
		m := discoveryToMap(n)
		if classified {
			names := public[n.Name]
			if names == nil {
				names = []string{}
			}
			m["publicIngressFirewalls"] = names
		}
		out = append(out, m)
	}
	return out
}

// urlMapsWithBackends returns the URL maps as maps, each joined to:
//
//   - its default backend service (when backendsOK): the service's
//     protocol and load-balancing scheme, as
//     defaultServiceProtocol / defaultServiceLoadBalancingScheme. A
//     default that is a backend bucket has neither, so nothing is set.
//   - the HTTPS proxies that point at it (when proxiesOK):
//     httpsProxyCount and sslPolicies, the SSL policy reference of each
//     proxy that sets one ([] when none). A proxy with no SSL policy
//     uses the GCP default policy.
//
// A join that could not run leaves its keys out, so the extractor can
// tell "not fetched" from "none".
func urlMapsWithBackends(maps []*computeapi.UrlMap, backends []*computeapi.BackendService, backendsOK bool, proxies []*computeapi.TargetHttpsProxy, proxiesOK bool) []map[string]any {
	byName := map[string]*computeapi.BackendService{}
	for _, bs := range backends {
		if bs != nil {
			byName[bs.Name] = bs
		}
	}
	proxiesByMap := map[string][]*computeapi.TargetHttpsProxy{}
	for _, p := range proxies {
		if p != nil {
			k := lastPathSegment(p.UrlMap)
			proxiesByMap[k] = append(proxiesByMap[k], p)
		}
	}

	out := make([]map[string]any, 0, len(maps))
	for _, um := range maps {
		if um == nil {
			continue
		}
		m := discoveryToMap(um)
		if backendsOK && strings.Contains(um.DefaultService, "/backendServices/") {
			if bs, ok := byName[lastPathSegment(um.DefaultService)]; ok {
				if bs.Protocol != "" {
					m["defaultServiceProtocol"] = bs.Protocol
				}
				if bs.LoadBalancingScheme != "" {
					m["defaultServiceLoadBalancingScheme"] = bs.LoadBalancingScheme
				}
			}
		}
		if proxiesOK {
			ps := proxiesByMap[um.Name]
			policies := []string{}
			for _, p := range ps {
				if p.SslPolicy != "" {
					policies = append(policies, p.SslPolicy)
				}
			}
			m["httpsProxyCount"] = len(ps)
			m["sslPolicies"] = policies
		}
		out = append(out, m)
	}
	return out
}

// withPolicyRules makes sure each listed Cloud Armor policy carries its
// rules, so extractGCPCloudArmorConfig can read the default action
// (the rule at the lowest priority, 2147483647). A policy that came
// back from the list without rules is described with get, up to
// cloudArmorDescribeCap policies. A failed get keeps the listed policy
// as it was. The result is never nil, so an empty list marshals as [].
func withPolicyRules(ctx context.Context, policies []*computeapi.SecurityPolicy, get func(context.Context, string) (*computeapi.SecurityPolicy, error)) []*computeapi.SecurityPolicy {
	out := make([]*computeapi.SecurityPolicy, 0, len(policies))
	described := 0
	for _, p := range policies {
		if p == nil {
			continue
		}
		if len(p.Rules) == 0 && p.Name != "" && described < cloudArmorDescribeCap {
			described++
			full, err := get(ctx, p.Name)
			switch {
			case err != nil:
				log.Printf("[discovery/gcp cloudarmor] list-policies: securityPolicies.get %q failed (non-fatal, rules omitted): %v", p.Name, err)
			case full != nil:
				p = full
			}
		}
		out = append(out, p)
	}
	return out
}
