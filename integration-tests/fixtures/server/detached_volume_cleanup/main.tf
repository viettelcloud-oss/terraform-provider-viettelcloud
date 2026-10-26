# This fixture adopts a detached volume so test teardown deletes it once.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}
variable "volume_id" { type = string }
variable "volume_name" { type = string }
variable "volume_size" { type = number }
variable "volume_type" { type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }) }
variable "zone" { type = string }

import {
  to = viettelcloud_volume.kept
  id = var.volume_id
}
resource "viettelcloud_volume" "kept" {
  name = var.volume_name
  size = var.volume_size
  zone = { name = var.zone }
  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }
}
output "volume" {
  value = viettelcloud_volume.kept
}
