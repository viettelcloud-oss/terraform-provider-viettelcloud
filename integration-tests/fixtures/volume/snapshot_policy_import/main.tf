# This plan-only fixture imports the volume that the "snapshot-policy-import-source"
# chain creates. The chain that created the volume owns cleanup, so do not apply
# this fixture. It configures only the attributes whose omission would plan a
# change, and takes their values from the creating run.
terraform {
  required_version = ">= 1.16"
  required_providers { viettelcloud = { source = "viettelcloud-oss/viettelcloud" } }
}
variable "volume_id" { type = string }
variable "volume_name" { type = string }
variable "volume_size" { type = number }
variable "volume_type" { type = object({ name = string }) }
variable "snapshot_policy_id" { type = string }
import {
  to = viettelcloud_volume.imported
  id = var.volume_id
}
resource "viettelcloud_volume" "imported" {
  name            = var.volume_name
  size            = var.volume_size
  create_from     = { source_type = "empty", volume_type = var.volume_type }
  snapshot_policy = { id = var.snapshot_policy_id }
}
