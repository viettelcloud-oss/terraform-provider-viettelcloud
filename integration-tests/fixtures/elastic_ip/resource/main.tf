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

variable "description" {
  type    = string
  default = null
}

variable "enable_ipv6" {
  type    = bool
  default = null
}

resource "viettelcloud_elastic_ip" "elastic_ip" {
  description = var.description
  enable_ipv6 = var.enable_ipv6
  region      = { name = var.region }
}

output "elastic_ip_id" {
  value = viettelcloud_elastic_ip.elastic_ip.id
}
