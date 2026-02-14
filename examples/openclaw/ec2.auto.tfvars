ec2_project              = "openclaw"
ec2_region               = "us-west-2"
ec2_instance_type        = "t3.medium"
ec2_associate_public_ip  = true
ec2_custom_ingress_ports = [22, 18789]

# Ubuntu 24.04 LTS (us-west-2) — update for your region
# ec2_ami_id = "ami-0aef57767f5404a3c"

# Paste your SSH public key to enable SSH access:
# ec2_ssh_public_key = "ssh-ed25519 AAAA..."
#
# Or access the instance via SSM Session Manager:
#   aws ssm start-session --target <instance-id>

ec2_user_data = <<-USERDATA
#!/bin/bash
set -euo pipefail

# Install Node.js 22 (OpenClaw hard requirement)
curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
apt-get install -y nodejs git

# Install OpenClaw
npm install -g openclaw@latest

# Generate gateway auth token
OPENCLAW_TOKEN=$(openssl rand -hex 32)
echo "$OPENCLAW_TOKEN" > /home/ubuntu/.openclaw-token
chown ubuntu:ubuntu /home/ubuntu/.openclaw-token
chmod 600 /home/ubuntu/.openclaw-token

# Configure OpenClaw for LAN access with token auth
sudo -u ubuntu bash -c "
  export OPENCLAW_GATEWAY_BIND=lan
  export OPENCLAW_GATEWAY_TOKEN=$OPENCLAW_TOKEN
  openclaw daemon install --system
"

# Firewall
ufw allow 22/tcp
ufw allow 18789/tcp
ufw --force enable
USERDATA
