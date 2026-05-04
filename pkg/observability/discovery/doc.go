// Package discovery hosts the AWS- and GCP-side resource enumeration
// inspectors used by the observability panel and drift detector.
//
// Empty-result contract (#255):
//
// Every slice-shaped return MUST marshal as a JSON array (`[]` or
// `[…]`), never JSON `null`. See CONTRIBUTING.md in this directory for
// the two patterns (uninitialized loop accumulator; direct SDK-slice
// passthrough), the new-inspector checklist, and the pre-release live
// probes (TestLive255_*) that pin the contract end-to-end.
package discovery
