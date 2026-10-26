terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "region" {
  type    = string
  default = "vn-central-1"
}

variable "dual_stack_description" {
  type    = string
  default = "tf-test-ds-eip-dual-stack"
}

variable "ipv4_only_description" {
  type    = string
  default = "tf-test-ds-eip-ipv4-only"
}

# Two Elastic IPs so every filter has to discriminate between candidates.
resource "viettelcloud_elastic_ip" "dual_stack" {
  description = var.dual_stack_description
  enable_ipv6 = true
  region      = { name = var.region }
}

resource "viettelcloud_elastic_ip" "ipv4_only" {
  description = var.ipv4_only_description
  region      = { name = var.region }
}

data "viettelcloud_elastic_ip" "by_id" {
  id = viettelcloud_elastic_ip.dual_stack.id
}

data "viettelcloud_elastic_ip" "by_ip_address" {
  ip_address = viettelcloud_elastic_ip.ipv4_only.ip_address
}

data "viettelcloud_elastic_ip" "by_ipv6_address" {
  ipv6_address = viettelcloud_elastic_ip.dual_stack.ipv6_address
}

# status, region, and available are not unique on their own, so each is combined
# with the unique address.
data "viettelcloud_elastic_ip" "by_status_and_ip_address" {
  status     = viettelcloud_elastic_ip.ipv4_only.status
  ip_address = viettelcloud_elastic_ip.ipv4_only.ip_address
}

data "viettelcloud_elastic_ip" "by_region_and_ip_address" {
  region     = var.region
  ip_address = viettelcloud_elastic_ip.dual_stack.ip_address
}

# A freshly created Elastic IP is held by no server, so it is available.
data "viettelcloud_elastic_ip" "by_available_and_ip_address" {
  available  = true
  ip_address = viettelcloud_elastic_ip.ipv4_only.ip_address
}

# Untrimmed and re-cased criteria must resolve the same Elastic IP and stay in
# state as configured.
data "viettelcloud_elastic_ip" "by_all_filters" {
  id           = upper(viettelcloud_elastic_ip.dual_stack.id)
  ip_address   = " ${viettelcloud_elastic_ip.dual_stack.ip_address} "
  ipv6_address = " ${viettelcloud_elastic_ip.dual_stack.ipv6_address} "
  region       = " ${var.region} "
  available    = true
}

output "dual_stack_id" {
  value = viettelcloud_elastic_ip.dual_stack.id
}

output "ipv4_only_id" {
  value = viettelcloud_elastic_ip.ipv4_only.id
}
