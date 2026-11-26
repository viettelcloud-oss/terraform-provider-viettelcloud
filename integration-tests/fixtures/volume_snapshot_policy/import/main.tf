# Plan-only fixture for the import chain of
# tests/volume_snapshot_policy_resource.tftest.hcl. It imports the policy that
# run "createVolumeSnapshotPolicy_importChainFixture_created" created, rather
# than creating one of its own.
#
# Never apply it. That policy already belongs to state_key "import", so a
# second owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "policy_id" {
  type = string
}

variable "name" {
  type = string
}

variable "region" {
  type = string
}

variable "days_of_week" {
  type = set(string)
}

variable "time" {
  type = string
}

variable "status" {
  type = string
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_volume_snapshot_policy.imported
  id = var.policy_id
}

# Only the required attributes are set, fed from the state of the run that
# created the policy. description and retention are optional and computed, so
# leaving them unset keeps whatever the import refresh read.
resource "viettelcloud_volume_snapshot_policy" "imported" {
  name   = var.name
  region = { name = var.region }
  status = var.status

  schedule = {
    days_of_week = var.days_of_week
    time         = var.time
  }
}
