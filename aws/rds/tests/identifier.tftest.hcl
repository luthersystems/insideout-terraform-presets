mock_provider "aws" {}

# Regression for #955. An RDS identifier has at most 63 characters. A
# Reliable project name ("io-" + 12 characters) made the replica identifier
# 66 characters, and AWS refused it at apply ("Invalid database identifier").

variables {
  region     = "us-west-2"
  vpc_id     = "vpc-12345"
  subnet_ids = ["subnet-aaa", "subnet-bbb"]
}

run "long_project_replica_fits" {
  command = plan

  variables {
    project            = "io-t5uhpzvbuokl"
    environment        = "prod"
    read_replica_count = 2
  }

  assert {
    condition     = alltrue([for r in aws_db_instance.replica : length(r.identifier) <= 63])
    error_message = "every replica identifier must have at most 63 characters."
  }
  assert {
    condition     = length(distinct([for r in aws_db_instance.replica : r.identifier])) == 2
    error_message = "replica identifiers must be different."
  }
  assert {
    condition     = alltrue([for r in aws_db_instance.replica : startswith(r.identifier, "io-t5uhpzvbuokl-")])
    error_message = "a replica identifier must keep the project prefix."
  }
  assert {
    condition     = alltrue([for r in aws_db_instance.replica : !strcontains(r.identifier, "--")])
    error_message = "a replica identifier must not have consecutive hyphens."
  }
}

run "short_project_replica_unchanged" {
  command = plan

  variables {
    project     = "rdsid"
    environment = "test"
  }

  assert {
    condition     = aws_db_instance.replica[0].identifier == "${aws_db_instance.primary.identifier}-replica-1"
    error_message = "a replica identifier that fits must not change (no replacement of existing replicas)."
  }
}
