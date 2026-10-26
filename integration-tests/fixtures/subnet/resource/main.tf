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
  type    = string
  default = null
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

output "subnet_id" {
  value = viettelcloud_subnet.subnet.id
}

output "vpc_id" {
  value = viettelcloud_vpc.vpc.id
}
