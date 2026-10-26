terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "region" {
  type = string
}

variable "vpc_name" {
  type = string
}

variable "vpc_cidr" {
  type = string
}

variable "subnet_index" {
  type    = number
  default = 0
}

variable "private_ip_description" {
  type    = string
  default = null
}

variable "private_ip_address" {
  type    = string
  default = null
}

variable "private_ip_mac_address" {
  type    = string
  default = null
}

variable "allowed_vips_mode" {
  type    = string
  default = "omit"
}

variable "use_uppercase_representations" {
  type    = bool
  default = false
}

locals {
  subnet_names = [
    "tf-test-private-ip-subnet-a",
    "tf-test-private-ip-subnet-b",
  ]
  subnet_cidrs = [
    "10.80.1.0/24",
    "10.80.2.0/24",
  ]
}

resource "viettelcloud_vpc" "vpc" {
  name   = var.vpc_name
  cidr   = var.vpc_cidr
  region = { name = var.region }
}

resource "viettelcloud_subnet" "subnet" {
  count = length(local.subnet_cidrs)
  name  = local.subnet_names[count.index]
  cidr  = local.subnet_cidrs[count.index]
  vpc   = { id = viettelcloud_vpc.vpc.id }
}

resource "viettelcloud_private_ip" "private_ip" {
  subnet = {
    id = var.use_uppercase_representations ? upper(viettelcloud_subnet.subnet[var.subnet_index].id) : viettelcloud_subnet.subnet[var.subnet_index].id
  }
  description = var.private_ip_description
  ip_address  = var.private_ip_address
  mac_address = var.private_ip_mac_address != null && var.use_uppercase_representations ? upper(var.private_ip_mac_address) : var.private_ip_mac_address
  allowed_vips = var.allowed_vips_mode == "omit" ? null : var.allowed_vips_mode == "fixture" ? [
    { id = var.use_uppercase_representations ? upper(viettelcloud_private_ip.vip.id) : viettelcloud_private_ip.vip.id }
  ] : []
}

resource "viettelcloud_private_ip" "vip" {
  subnet      = { id = viettelcloud_subnet.subnet[var.subnet_index].id }
  description = "Private IP used as an allowed VIP integration fixture"
}

# Keep a second resource on the omitted create path while the primary resource
# exercises the create-with-allowed-VIPs path.
resource "viettelcloud_private_ip" "without_allowed_vips" {
  subnet      = { id = viettelcloud_subnet.subnet[0].id }
  description = "Private IP with allowed VIPs omitted at creation"
}

output "private_ip_id" {
  value = viettelcloud_private_ip.private_ip.id
}

output "subnet_ids" {
  value = viettelcloud_subnet.subnet[*].id
}

output "fixture_vip_id" {
  value = viettelcloud_private_ip.vip.id
}

output "private_ip_without_allowed_vips_id" {
  value = viettelcloud_private_ip.without_allowed_vips.id
}

output "vpc_id" {
  value = viettelcloud_vpc.vpc.id
}
