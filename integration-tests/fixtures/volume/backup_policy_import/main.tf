# This plan-only fixture imports the volume of the "backup-policy" chain while a
# policy is attached. It declares backup_policy, as an import of a volume that
# has a policy needs it. Do not apply this fixture: the "backup-policy" chain
# owns cleanup.
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

variable "policy_id" {
  type = string
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

  backup_policy = { id = var.policy_id }
}
