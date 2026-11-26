# This fixture reads the existing network that the Load Balancer tests use and
# creates nothing, so the suite costs no VPC or subnet quota. The second VPC is
# the target of run "changeVpc_sameRegion_loadBalancerReplaced".
#
# The lookups fail early when a variable names a missing object, and the run
# assertions reject a subnet outside vpc_id or a VPC in another region.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "vpc_id" {
  type = string
}

variable "subnet_id" {
  type = string
}

variable "replacement_vpc_id" {
  type = string
}

data "viettelcloud_vpc" "shared" {
  id = var.vpc_id
}

data "viettelcloud_subnet" "shared" {
  id = var.subnet_id
}

data "viettelcloud_vpc" "replacement" {
  id = var.replacement_vpc_id
}

output "vpc_id" {
  value = data.viettelcloud_vpc.shared.id
}

output "vpc_name" {
  value = data.viettelcloud_vpc.shared.name
}

output "vpc_region" {
  value = data.viettelcloud_vpc.shared.region
}

output "subnet_id" {
  value = data.viettelcloud_subnet.shared.id
}

output "subnet_vpc_id" {
  value = data.viettelcloud_subnet.shared.vpc_id
}

output "replacement_vpc_id" {
  value = data.viettelcloud_vpc.replacement.id
}

output "replacement_vpc_name" {
  value = data.viettelcloud_vpc.replacement.name
}

output "replacement_vpc_region" {
  value = data.viettelcloud_vpc.replacement.region
}
