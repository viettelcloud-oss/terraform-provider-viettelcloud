# The security group that owns the rules. Reference viettelcloud_security_group.id
# once that resource manages the group.
variable "security_group_id" {
  type        = string
  description = "Security group ID (UUID) to attach the rules to."
}

# Allow inbound SSH from anywhere.
resource "viettelcloud_security_group_rule" "ingress_ssh" {
  security_group   = { id = var.security_group_id }
  direction        = "ingress"
  protocol         = "tcp"
  port_range_min   = 22
  port_range_max   = 22
  remote_ip_prefix = "0.0.0.0/0"
  description      = "Allow inbound SSH"
}

# Allow outbound HTTPS to anywhere.
resource "viettelcloud_security_group_rule" "egress_https" {
  security_group   = { id = var.security_group_id }
  direction        = "egress"
  protocol         = "tcp"
  port_range_min   = 443
  port_range_max   = 443
  remote_ip_prefix = "0.0.0.0/0"
  description      = "Allow outbound HTTPS"
}
