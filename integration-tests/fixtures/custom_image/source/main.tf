# This fixture owns bootable source volumes until the image tests finish.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "source_keys" { type = set(string) }
variable "name_prefix" { type = string }
variable "image_name" { type = string }
variable "volume_type" { type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }) }
variable "zone" { type = string }
variable "volume_size" { type = number }

resource "viettelcloud_volume" "source" {
  for_each = var.source_keys
  name     = "${var.name_prefix}-${each.key}"
  size     = var.volume_size
  zone     = { name = var.zone }
  create_from = {
    source_type = "image"
    image       = { name = var.image_name }
    volume_type = var.volume_type
  }
}

output "volumes" { value = viettelcloud_volume.source }
