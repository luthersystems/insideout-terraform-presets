# Hermetic plan tests for the GPU AMI selection path added in #759.
#
# gpu_enabled=true selects the AWS Deep Learning Base GPU AMI (Amazon Linux
# 2023, NVIDIA driver baked in) so a g5/g6/p4d/p5 instance comes up with
# /dev/nvidia* present, instead of the plain OS AMI. GPU AMIs are x86_64-only,
# so gpu_enabled=true requires arch=x86_64 — the variable validation rejects
# the arm64 mismatch (the #207 class: wrong AMI for the chosen hardware).
#
# The in-cluster / on-host NVIDIA device plugin and CUDA runtime layering is
# app-layer and out of preset scope — this module only provisions a
# GPU-capable instance with the driver-bearing AMI.

mock_provider "aws" {}

variables {
  project     = "test"
  region      = "us-east-1"
  environment = "test"
  vpc_id      = "vpc-12345678"
  subnet_id   = "subnet-12345678"
}

run "gpu_enabled_selects_gpu_ami" {
  command = plan

  override_data {
    target = data.aws_iam_policy_document.ec2_assume_role
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  override_data {
    target = data.aws_ami.gpu[0]
    values = {
      id = "ami-0gpu00000000000ab"
    }
  }

  variables {
    gpu_enabled   = true
    arch          = "x86_64"
    os_type       = "amazon-linux"
    instance_type = "g5.xlarge"
  }

  assert {
    condition     = aws_instance.this.ami == "ami-0gpu00000000000ab"
    error_message = "gpu_enabled must select the GPU AMI (data.aws_ami.gpu) for the instance (#759)"
  }
  assert {
    condition     = length(data.aws_ami.al2023) == 0 && length(data.aws_ami.ubuntu) == 0
    error_message = "gpu_enabled must short-circuit the os_type AMI lookups (#759)"
  }
  assert {
    condition     = aws_instance.this.instance_type == "g5.xlarge"
    error_message = "instance_type must be the requested GPU type g5.xlarge"
  }
}

# Explicit ami_id still wins over the GPU AMI lookup.
run "explicit_ami_id_overrides_gpu" {
  command = plan

  override_data {
    target = data.aws_iam_policy_document.ec2_assume_role
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  variables {
    gpu_enabled   = true
    arch          = "x86_64"
    instance_type = "g5.xlarge"
    ami_id        = "ami-deadbeefdeadbeef0"
  }

  assert {
    condition     = aws_instance.this.ami == "ami-deadbeefdeadbeef0"
    error_message = "explicit ami_id must override the GPU AMI lookup"
  }
  assert {
    condition     = length(data.aws_ami.gpu) == 0
    error_message = "explicit ami_id must short-circuit the GPU AMI data source"
  }
}

# Arch/AMI mismatch (#207 class): GPU AMIs are x86_64-only, so gpu_enabled
# with arch=arm64 must fail validation rather than silently provisioning an
# instance with an incompatible AMI.
run "gpu_with_arm64_fails_validation" {
  command = plan

  override_data {
    target = data.aws_iam_policy_document.ec2_assume_role
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  variables {
    gpu_enabled = true
    arch        = "arm64"
  }

  expect_failures = [var.gpu_enabled]
}

# os_type override (#759): gpu_enabled selects the Amazon Linux 2023 GPU AMI
# and ignores os_type. Previously os_type="ubuntu" was silently dropped on the
# GPU path — a caller asking for Ubuntu got an Amazon Linux node with no
# warning. The aws_instance precondition now rejects the combination loudly so
# the mismatch surfaces at plan instead of booting an unexpected OS.
run "gpu_with_ubuntu_os_type_fails_precondition" {
  command = plan

  override_data {
    target = data.aws_iam_policy_document.ec2_assume_role
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  override_data {
    target = data.aws_ami.gpu[0]
    values = {
      id = "ami-0gpu00000000000ab"
    }
  }

  variables {
    gpu_enabled   = true
    arch          = "x86_64"
    os_type       = "ubuntu"
    instance_type = "g5.xlarge"
  }

  expect_failures = [aws_instance.this]
}

# Explicit ami_id with gpu_enabled + os_type="ubuntu" is allowed: the caller
# supplied their own (e.g. Ubuntu GPU) AMI, so the precondition's ami_id==null
# guard does not trip. Locks that the precondition only fires on the
# silent-override path, not on the bring-your-own-AMI path.
run "gpu_with_ubuntu_and_explicit_ami_id_passes" {
  command = plan

  override_data {
    target = data.aws_iam_policy_document.ec2_assume_role
    values = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  variables {
    gpu_enabled   = true
    arch          = "x86_64"
    os_type       = "ubuntu"
    instance_type = "g5.xlarge"
    ami_id        = "ami-ubuntugpu0000000"
  }

  assert {
    condition     = aws_instance.this.ami == "ami-ubuntugpu0000000"
    error_message = "explicit ami_id must be honored even with gpu_enabled + os_type=ubuntu"
  }
}
