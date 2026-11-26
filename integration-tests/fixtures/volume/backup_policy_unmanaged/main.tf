# This fixture imports the volume of the "backup-policy" chain into a second
# state, so that it can change the volume outside the state of that chain.
# backup_policy_id attaches that policy, and null removes the policy that the
# volume has. The "backup-policy" chain still owns cleanup: it attaches the
# first policy again and then destroys the volume, so the teardown of this state
# finds the volume gone.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "volume_id" {
  type = string
}

variable "volume_name" {
  type = string
}

variable "volume_size" {
  type = number
}

variable "volume_type_name" {
  type = string
}

variable "backup_policy_id" {
  description = "ID of the backup policy to attach. Null removes the policy."
  type        = string
  default     = null
}

import {
  to = viettelcloud_volume.imported
  id = var.volume_id
}

resource "viettelcloud_volume" "imported" {
  name = var.volume_name
  size = var.volume_size

  create_from = {
    source_type = "empty"
    volume_type = { name = var.volume_type_name }
  }

  backup_policy = var.backup_policy_id == null ? null : { id = var.backup_policy_id }
}
