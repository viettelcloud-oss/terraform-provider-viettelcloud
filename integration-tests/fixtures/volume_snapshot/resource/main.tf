terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "volume_type" { type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }) }
variable "zone" { type = string }
variable "name" { type = string }
variable "source_prefix" {
  type    = string
  default = "lifecycle"
}
variable "description" {
  type    = string
  default = null
}
variable "source_key" {
  type    = string
  default = "first"
}

resource "viettelcloud_volume" "source" {
  for_each = toset(["first", "second"])
  name     = "tf-test-snapshot-source-${var.source_prefix}-${each.key}"
  size     = 30
  zone     = { name = var.zone }
  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }
}

resource "viettelcloud_volume_snapshot" "subject" {
  name        = var.name
  description = var.description
  volume      = { id = viettelcloud_volume.source[var.source_key].id }
}

output "snapshot" { value = viettelcloud_volume_snapshot.subject }
output "source_id" { value = viettelcloud_volume.source[var.source_key].id }
