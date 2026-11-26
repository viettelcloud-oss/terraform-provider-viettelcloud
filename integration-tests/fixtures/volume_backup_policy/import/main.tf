# Plan-only fixture for the import chain of
# tests/volume_backup_policy_resource.tftest.hcl. It imports the policy that
# run "createVolumeBackupPolicy_importChainFixture_policyCreated" created,
# rather than creating one of its own.
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

variable "status" {
  type = string
}

variable "schedule" {
  type = object({
    execution_strategy = string
    frequency          = optional(number)
    frequency_unit     = optional(string)
    first_execution    = optional(string)
    days_of_month      = optional(set(number))
    time               = optional(string)
  })
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_volume_backup_policy.imported
  id = var.policy_id
}

# Only the required attributes are set, fed from the state of the run that
# created the policy. description and retention are optional and computed, so
# leaving them unset keeps whatever the import refresh read.
resource "viettelcloud_volume_backup_policy" "imported" {
  name     = var.name
  region   = { name = var.region }
  status   = var.status
  schedule = var.schedule
}
