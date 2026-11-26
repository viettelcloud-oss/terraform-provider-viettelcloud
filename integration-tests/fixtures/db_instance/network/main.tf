# This fixture creates an isolated VPC and subnet for the DB instance and DB
# instance backup suites. Each suite runs it first under its own state_key, so
# teardown removes the network after every chain releases its instances.
# with_replacement_subnet adds a second subnet in the same VPC, which the
# subnet replacement run of the DB instance resource suite selects.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "name" { type = string }
variable "region" { type = string }
variable "cidr" { type = string }

variable "with_replacement_subnet" {
  type    = bool
  default = false
}

resource "viettelcloud_vpc" "shared" {
  name   = var.name
  cidr   = var.cidr
  region = { name = var.region }
}

resource "viettelcloud_subnet" "shared" {
  name = var.name
  cidr = cidrsubnet(var.cidr, 8, 1)
  vpc  = { id = viettelcloud_vpc.shared.id }
}

resource "viettelcloud_subnet" "replacement" {
  count = var.with_replacement_subnet ? 1 : 0
  name  = "${var.name}-replacement"
  cidr  = cidrsubnet(var.cidr, 8, 2)
  vpc   = { id = viettelcloud_vpc.shared.id }
}

output "subnet_id" {
  value = viettelcloud_subnet.shared.id
}

output "replacement_subnet_id" {
  value = var.with_replacement_subnet ? viettelcloud_subnet.replacement[0].id : null
}

output "subnet" {
  value = viettelcloud_subnet.shared
}

output "vpc" {
  value = viettelcloud_vpc.shared
}
