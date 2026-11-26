# Deletes a policy outside the state that created it, for the drift chain of
# tests/filesystem_snapshot_policy_resource.tftest.hcl. With adopt = true it
# imports the policy into its own state key. With adopt = false the resource
# leaves the configuration, so apply destroys the policy on the backend while
# the creating state still records it.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "adopt" {
  type = bool
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
  type = list(string)
}

variable "time" {
  type = string
}

variable "status" {
  type = string
}

locals {
  adopted = var.adopt ? toset([var.policy_id]) : toset([])
}

import {
  for_each = local.adopted
  to       = viettelcloud_fs_filesystem_snapshot_policy.adopted[each.key]
  id       = each.value
}

resource "viettelcloud_fs_filesystem_snapshot_policy" "adopted" {
  for_each = local.adopted

  name         = var.name
  region       = { name = var.region }
  days_of_week = var.days_of_week
  time         = var.time
  status       = var.status
}
