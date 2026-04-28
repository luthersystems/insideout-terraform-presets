package main

import "strings"

// acronyms is the dictionary of strings that should appear ALL-CAPS in
// generated Go field names rather than Title-cased. The matcher is exact
// (whole word, lowercased) and runs over each underscore-delimited part of
// a Terraform field name. The intent is to produce idiomatic Go names like
// AWSSQSQueue and KMSMasterKeyID instead of AwsSqsQueue and KmsMasterKeyId.
//
// Add entries here when generated names start looking unidiomatic. Avoid
// adding ambiguous strings (e.g. "io") — they become noisy false-positives.
var acronyms = func() map[string]string {
	m := map[string]string{}
	for _, a := range []string{
		"ACL",
		"API",
		"ARN",
		"AWS",
		"CDN",
		"CIDR",
		"CPU",
		"CSV",
		"DNS",
		"EBS",
		"ECR",
		"ECS",
		"EFS",
		"EKS",
		"FIFO",
		"GCP",
		"GCS",
		"GKE",
		"GRPC",
		"HTTP",
		"HTTPS",
		"IAM",
		"ID",
		"IDS",
		"IPV4",
		"IPV6",
		"JSON",
		"JWT",
		"KMS",
		"MD5",
		"MFA",
		"OIDC",
		"PEM",
		"PKI",
		"RAM",
		"RDS",
		"S3",
		"SDK",
		"SHA",
		"SHA1",
		"SHA256",
		"SMS",
		"SNS",
		"SQS",
		"SSE",
		"SSL",
		"TCP",
		"TLS",
		"TTL",
		"UDP",
		"URI",
		"URL",
		"UUID",
		"VPC",
		"WAF",
		"XML",
		"YAML",
	} {
		m[strings.ToLower(a)] = a
	}
	return m
}()

// acronymOf reports whether part (lowercase) is a known acronym and
// returns the canonical uppercase form if so.
func acronymOf(part string) (string, bool) {
	a, ok := acronyms[part]
	return a, ok
}
