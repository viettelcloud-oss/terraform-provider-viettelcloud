# This fixture creates an isolated VPC so server test subnets have their own
# address space.
#
# The network setup runs before the server chains so teardown destroys the VPC
# after every chain releases its subnets.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "name" {
  type    = string
  default = "tf-test-server-vpc"
}

variable "cidr" {
  type    = string
  default = "10.36.0.0/16"
}

variable "region" {
  type    = string
  default = "vn-central-1"
}

resource "viettelcloud_vpc" "shared" {
  name   = var.name
  cidr   = var.cidr
  region = { name = var.region }
}

output "vpc_id" {
  value = viettelcloud_vpc.shared.id
}

output "vpc_cidr" {
  value = viettelcloud_vpc.shared.cidr
}
