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

variable "name" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

variable "cidr" {
  type = string
}

resource "viettelcloud_vpc" "vpc" {
  name        = var.name
  description = var.description
  cidr        = var.cidr
  region      = { name = var.region }
}

data "viettelcloud_vpc" "vpc" {
  id = viettelcloud_vpc.vpc.id
}

output "vpc_id" {
  value = viettelcloud_vpc.vpc.id
}
