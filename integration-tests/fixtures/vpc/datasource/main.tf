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

variable "vpc1_name" {
  type    = string
  default = "tf-test-ds-vpc-1"
}

variable "vpc1_cidr" {
  type    = string
  default = "10.80.0.0/16"
}

variable "vpc1_description" {
  type    = string
  default = "VPC 1 for data source test"
}

resource "viettelcloud_vpc" "vpc1" {
  name        = var.vpc1_name
  description = var.vpc1_description
  cidr        = var.vpc1_cidr
  region      = { name = var.region }
}

data "viettelcloud_vpc" "by_id" {
  id = viettelcloud_vpc.vpc1.id
}

data "viettelcloud_vpc" "by_name" {
  name = viettelcloud_vpc.vpc1.name
}

data "viettelcloud_vpc" "by_cidr" {
  cidr = viettelcloud_vpc.vpc1.cidr
}

data "viettelcloud_vpc" "by_cidr_and_region" {
  cidr   = viettelcloud_vpc.vpc1.cidr
  region = var.region
}

data "viettelcloud_vpc" "by_all_filters" {
  id     = viettelcloud_vpc.vpc1.id
  name   = viettelcloud_vpc.vpc1.name
  cidr   = viettelcloud_vpc.vpc1.cidr
  region = var.region
}

output "vpc1_id" {
  value = viettelcloud_vpc.vpc1.id
}
