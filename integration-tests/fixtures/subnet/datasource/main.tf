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

variable "subnet_name" {
  type = string
}

variable "subnet_description" {
  type = string
}

variable "subnet_cidr" {
  type = string
}

resource "viettelcloud_vpc" "vpc" {
  name   = var.vpc_name
  cidr   = var.vpc_cidr
  region = { name = var.region }
}

resource "viettelcloud_subnet" "subnet" {
  name        = var.subnet_name
  description = var.subnet_description
  cidr        = var.subnet_cidr
  vpc         = { id = viettelcloud_vpc.vpc.id }
}

data "viettelcloud_subnet" "by_id" {
  id = viettelcloud_subnet.subnet.id
}

data "viettelcloud_subnet" "by_name" {
  name = viettelcloud_subnet.subnet.name
}

data "viettelcloud_subnet" "by_cidr" {
  cidr = viettelcloud_subnet.subnet.cidr
}

data "viettelcloud_subnet" "by_vpc_id" {
  vpc_id = viettelcloud_subnet.subnet.vpc.id
}

data "viettelcloud_subnet" "by_vpc_name" {
  vpc_name = viettelcloud_subnet.subnet.vpc_name
}

data "viettelcloud_subnet" "by_name_and_vpc" {
  name     = viettelcloud_subnet.subnet.name
  vpc_name = viettelcloud_subnet.subnet.vpc_name
}

data "viettelcloud_subnet" "by_cidr_and_region" {
  cidr   = viettelcloud_subnet.subnet.cidr
  region = var.region
}

data "viettelcloud_subnet" "by_all_filters" {
  id       = upper(viettelcloud_subnet.subnet.id)
  name     = "  ${viettelcloud_subnet.subnet.name}  "
  cidr     = " ${viettelcloud_subnet.subnet.cidr} "
  vpc_id   = upper(viettelcloud_vpc.vpc.id)
  vpc_name = " ${viettelcloud_vpc.vpc.name} "
  region   = " ${var.region} "
}

output "subnet_id" {
  value = viettelcloud_subnet.subnet.id
}
