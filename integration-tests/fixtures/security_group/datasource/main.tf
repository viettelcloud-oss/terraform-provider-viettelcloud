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

variable "sg1_name" {
  type    = string
  default = "tf-test-ds-sg-1"
}

variable "sg1_description" {
  type    = string
  default = "Security group 1 for data source test"
}

resource "viettelcloud_security_group" "sg1" {
  name        = var.sg1_name
  description = var.sg1_description
  region      = { name = var.region }
}

data "viettelcloud_security_group" "by_id" {
  id = viettelcloud_security_group.sg1.id
}

data "viettelcloud_security_group" "by_name" {
  name = viettelcloud_security_group.sg1.name
}

data "viettelcloud_security_group" "by_name_and_region" {
  name   = viettelcloud_security_group.sg1.name
  region = var.region
}

data "viettelcloud_security_group" "by_all_filters" {
  id     = viettelcloud_security_group.sg1.id
  name   = viettelcloud_security_group.sg1.name
  region = var.region
}

data "viettelcloud_security_group" "by_is_default" {
  is_default = true
}

data "viettelcloud_security_group" "by_is_default_and_region" {
  is_default = true
  region     = var.region
}

output "sg1_id" {
  value = viettelcloud_security_group.sg1.id
}

output "default_sg_id" {
  value = data.viettelcloud_security_group.by_is_default.id
}
