terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "name" {
  type = string
}

variable "public_key" {
  type    = string
  default = null
}

resource "viettelcloud_key_pair" "key_pair" {
  name       = var.name
  public_key = var.public_key
}

data "viettelcloud_key_pair" "key_pair" {
  id = viettelcloud_key_pair.key_pair.id
}

output "key_pair_id" {
  value = viettelcloud_key_pair.key_pair.id
}

output "key_pair_name" {
  value = viettelcloud_key_pair.key_pair.name
}

output "key_pair_fingerprint" {
  value = viettelcloud_key_pair.key_pair.fingerprint
}

output "key_pair_public_key" {
  value = viettelcloud_key_pair.key_pair.public_key
}

output "key_pair_type" {
  value = viettelcloud_key_pair.key_pair.type
}

output "key_pair_private_key" {
  value     = viettelcloud_key_pair.key_pair.private_key
  sensitive = true
}
