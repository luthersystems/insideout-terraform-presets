mock_provider "aws" {}

# Issue #586: aws/acm preset shape tests. Verifies that the certificate is
# always created with DNS validation, that create_validation gates the
# aws_acm_certificate_validation resource, and that wildcard / SAN inputs
# survive validation.

run "acm_minimum_inputs_creates_dns_cert" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    domain_name = "example.com"
  }

  assert {
    condition     = aws_acm_certificate.this.validation_method == "DNS"
    error_message = "ACM cert must always use DNS validation in this preset (EMAIL is out of scope)."
  }

  assert {
    condition     = aws_acm_certificate.this.domain_name == "example.com"
    error_message = "domain_name input should pass through to the cert resource."
  }

  assert {
    condition     = length(aws_acm_certificate_validation.this) == 0
    error_message = "create_validation defaults to false, so aws_acm_certificate_validation must NOT be created on minimum inputs."
  }
}

run "acm_wildcard_with_sans" {
  command = plan

  variables {
    project                   = "test"
    region                    = "us-east-1"
    environment               = "test"
    domain_name               = "*.example.com"
    subject_alternative_names = ["example.com", "api.example.com"]
  }

  assert {
    condition     = aws_acm_certificate.this.domain_name == "*.example.com"
    error_message = "Wildcard domain_name should be accepted."
  }

  assert {
    condition     = length(aws_acm_certificate.this.subject_alternative_names) == 2
    error_message = "SAN list should pass through unchanged."
  }
}

run "acm_create_validation_emits_validation_resource" {
  command = plan

  variables {
    project                 = "test"
    region                  = "us-east-1"
    environment             = "test"
    domain_name             = "example.com"
    create_validation       = true
    validation_record_fqdns = ["_acme-challenge.example.com"]
  }

  assert {
    condition     = length(aws_acm_certificate_validation.this) == 1
    error_message = "create_validation = true must emit exactly one aws_acm_certificate_validation."
  }

  assert {
    condition     = aws_acm_certificate_validation.this[0].validation_record_fqdns == toset(["_acme-challenge.example.com"])
    error_message = "validation_record_fqdns input must pass through to the validation resource (as a set; the provider normalizes list -> set)."
  }
}

run "acm_default_key_algorithm_is_rsa_2048" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    domain_name = "example.com"
  }

  assert {
    condition     = aws_acm_certificate.this.key_algorithm == "RSA_2048"
    error_message = "Default key_algorithm should be RSA_2048 (broadest client compatibility)."
  }
}

run "acm_ct_logging_defaults_enabled" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    domain_name = "example.com"
  }

  assert {
    condition     = aws_acm_certificate.this.options[0].certificate_transparency_logging_preference == "ENABLED"
    error_message = "Certificate Transparency logging should default to ENABLED (browser/CT-log requirement for public certs)."
  }
}

# --- Negative cases: validation blocks must reject obvious misconfigurations
# at plan time so callers don't discover them at apply.

run "acm_rejects_domain_with_leading_hyphen" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    domain_name = "-bad.example.com"
  }

  expect_failures = [var.domain_name]
}

run "acm_rejects_more_than_9_sans" {
  command = plan

  variables {
    project                   = "test"
    region                    = "us-east-1"
    environment               = "test"
    domain_name               = "primary.example.com"
    subject_alternative_names = ["a.example.com", "b.example.com", "c.example.com", "d.example.com", "e.example.com", "f.example.com", "g.example.com", "h.example.com", "i.example.com", "j.example.com"]
  }

  expect_failures = [var.subject_alternative_names]
}

run "acm_rejects_invalid_key_algorithm" {
  command = plan

  variables {
    project       = "test"
    region        = "us-east-1"
    environment   = "test"
    domain_name   = "example.com"
    key_algorithm = "MD5"
  }

  expect_failures = [var.key_algorithm]
}

run "acm_rejects_malformed_validation_timeout" {
  command = plan

  variables {
    project            = "test"
    region             = "us-east-1"
    environment        = "test"
    domain_name        = "example.com"
    validation_timeout = "forty-five-minutes"
  }

  expect_failures = [var.validation_timeout]
}
