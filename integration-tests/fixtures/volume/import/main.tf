# This plan-only fixture uses name selectors to match the imported state.
# The selector import chain owns the volume.
# Do not apply this fixture because the selector import chain owns cleanup.
terraform {
  required_version = ">= 1.16"
  required_providers { viettelcloud = { source = "viettelcloud-oss/viettelcloud" } }
}
variable "volume_id" { type = string }
variable "volume_size" { type = number }
variable "volume_name" { type = string }
variable "image" { type = object({ name = string }) }
variable "volume_type" { type = object({ name = string }) }
import {
  to = viettelcloud_volume.imported
  id = var.volume_id
}
resource "viettelcloud_volume" "imported" {
  name        = var.volume_name
  size        = var.volume_size
  create_from = { source_type = "image", image = var.image, volume_type = var.volume_type }
}
