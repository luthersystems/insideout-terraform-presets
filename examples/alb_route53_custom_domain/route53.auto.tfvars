route53_project = "demo"
route53_region  = "us-east-1"

# Reserved RFC 6761 TLD — never resolvable, never registrable. Keeps a stray
# `terraform apply` in CI or a sandbox from creating a hosted zone for a real
# domain. Override at deploy time with the customer's actual apex.
route53_domain_name = "example.invalid"
route53_create_zone = true
