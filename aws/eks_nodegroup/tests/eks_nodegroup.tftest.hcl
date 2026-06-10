# Hermetic plan tests for the AMI-type auto-derive logic added in #207.
#
# Failure mode this guards against: when ami_type is omitted on
# aws_eks_node_group, the AWS provider defaults to AL2023_x86_64_STANDARD.
# Selecting a Graviton instance (c7g.large, m7g.xlarge, etc.) then produces
# an arch mismatch — workers never come up and aws-ebs-csi-driver / coredns
# sit DEGRADED until the addon timeout fires. The auto-derive picks
# AL2023_ARM_64_STANDARD when the first instance type's family ends in `g`
# (Graviton convention: c7g, m7g, r7g, t4g, c8g, m8g, r8g, ...) and falls
# back to AL2023_x86_64_STANDARD otherwise. var.ami_type overrides the
# derive so callers retain full control (Bottlerocket, GPU, etc.).

mock_provider "aws" {}

variables {
  project      = "test"
  region       = "us-east-1"
  environment  = "test"
  cluster_name = "test-cluster"
  subnet_ids   = ["subnet-aaa", "subnet-bbb"]
  desired_size = 2
  min_size     = 1
  max_size     = 3
  # Pass an existing role ARN so the module's count-gated IAM role + assume
  # policy resources don't run under mock_provider — the AWS mock returns
  # a non-JSON value for data.aws_iam_policy_document.mng_assume.json,
  # which the iam-role validator rejects. The AMI-type derive doesn't
  # depend on role creation, so this stays orthogonal to what's under test.
  node_role_arn = "arn:aws:iam::123456789012:role/test-eks-node-role"
}

run "arm_family_derives_arm_ami" {
  command = plan

  variables {
    instance_types = ["c7g.large"]
  }

  assert {
    condition     = output.ami_type == "AL2023_ARM_64_STANDARD"
    error_message = "Graviton family c7g.large must derive AL2023_ARM_64_STANDARD (#207)"
  }
  assert {
    condition     = aws_eks_node_group.this.ami_type == "AL2023_ARM_64_STANDARD"
    error_message = "node group resource ami_type must be set to AL2023_ARM_64_STANDARD when first instance type is c7g.large (#207)"
  }
}

run "x86_intel_family_derives_x86_ami" {
  command = plan

  variables {
    instance_types = ["c7i.large"]
  }

  assert {
    condition     = output.ami_type == "AL2023_x86_64_STANDARD"
    error_message = "x86 Intel family c7i.large must derive AL2023_x86_64_STANDARD"
  }
  assert {
    condition     = aws_eks_node_group.this.ami_type == "AL2023_x86_64_STANDARD"
    error_message = "node group resource ami_type must be set to AL2023_x86_64_STANDARD when first instance type is c7i.large"
  }
}

run "x86_general_family_derives_x86_ami" {
  command = plan

  variables {
    instance_types = ["m5.xlarge"]
  }

  assert {
    condition     = output.ami_type == "AL2023_x86_64_STANDARD"
    error_message = "x86 general-purpose family m5.xlarge must derive AL2023_x86_64_STANDARD"
  }
}

run "explicit_bottlerocket_arm_overrides_derive" {
  command = plan

  variables {
    instance_types = ["c7g.large"]
    ami_type       = "BOTTLEROCKET_ARM_64"
  }

  assert {
    condition     = output.ami_type == "BOTTLEROCKET_ARM_64"
    error_message = "explicit var.ami_type must override the auto-derived value"
  }
  assert {
    condition     = aws_eks_node_group.this.ami_type == "BOTTLEROCKET_ARM_64"
    error_message = "node group resource ami_type must reflect explicit var.ami_type override"
  }
}

run "mixed_family_first_instance_type_drives" {
  command = plan

  # EKS managed node groups require homogeneous architecture; the first
  # instance type in the list is the canonical choice. Pin that the
  # derive uses [0] and not some other heuristic (alphabetical, last, etc.).
  variables {
    instance_types = ["c7g.large", "c7g.xlarge", "m7g.large"]
  }

  assert {
    condition     = output.ami_type == "AL2023_ARM_64_STANDARD"
    error_message = "first instance type drives the AMI choice; ARM-led list must derive AL2023_ARM_64_STANDARD"
  }
}

run "bogus_ami_type_fails_validation" {
  command = plan

  variables {
    instance_types = ["c7i.large"]
    ami_type       = "FAKE_TYPE"
  }

  expect_failures = [var.ami_type]
}

# GPU node group (#759): an NVIDIA x86 instance family must auto-derive the
# NVIDIA managed AMI type so the node comes up GPU-capable. Without this, a
# g5.xlarge on the AL2023_x86_64_STANDARD default exposes no /dev/nvidia*
# devices and GPU pods sit Pending — the GPU analogue of the #207 arch
# mismatch.
run "gpu_nodegroup" {
  command = plan

  variables {
    instance_types = ["g5.xlarge"]
  }

  assert {
    condition     = output.ami_type == "AL2023_x86_64_NVIDIA"
    error_message = "GPU x86 family g5.xlarge must derive AL2023_x86_64_NVIDIA (#759)"
  }
  assert {
    condition     = aws_eks_node_group.this.ami_type == "AL2023_x86_64_NVIDIA"
    error_message = "node group resource ami_type must be AL2023_x86_64_NVIDIA for a GPU instance family (#759)"
  }
  assert {
    condition     = tolist(aws_eks_node_group.this.instance_types)[0] == "g5.xlarge"
    error_message = "node group must use the requested GPU instance type g5.xlarge"
  }
}

# p-family GPU instances also resolve the NVIDIA AMI.
run "gpu_p_family_derives_nvidia_ami" {
  command = plan

  variables {
    instance_types = ["p4d.24xlarge"]
  }

  assert {
    condition     = output.ami_type == "AL2023_x86_64_NVIDIA"
    error_message = "GPU p-family p4d.24xlarge must derive AL2023_x86_64_NVIDIA (#759)"
  }
}

# g5g is Graviton (ARM) + NVIDIA T4G. EKS has no ARM NVIDIA managed AMI type,
# so g5g stays on the ARM standard AMI — locks that the GPU allow-list does
# not steal ARM families ending in `g`.
run "g5g_arm_stays_arm_standard" {
  command = plan

  variables {
    instance_types = ["g5g.xlarge"]
  }

  assert {
    condition     = output.ami_type == "AL2023_ARM_64_STANDARD"
    error_message = "g5g (Graviton NVIDIA) must keep AL2023_ARM_64_STANDARD — no ARM NVIDIA managed AMI type exists (#759)"
  }
}

# Explicit var.ami_type still overrides the GPU auto-derive (e.g. picking the
# Bottlerocket NVIDIA variant).
run "explicit_bottlerocket_nvidia_overrides_gpu_derive" {
  command = plan

  variables {
    instance_types = ["g5.xlarge"]
    ami_type       = "BOTTLEROCKET_x86_64_NVIDIA"
  }

  assert {
    condition     = output.ami_type == "BOTTLEROCKET_x86_64_NVIDIA"
    error_message = "explicit var.ami_type must override the GPU auto-derive"
  }
}
