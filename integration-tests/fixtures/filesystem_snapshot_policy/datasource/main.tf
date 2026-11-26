# This fixture creates one filesystem snapshot policy and resolves it through
# the direct ID path, the name and region criteria, and every criterion in a
# non-canonical spelling.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "policy_name" {
  type = string
}

variable "policy_region" {
  type = string
}

resource "viettelcloud_fs_filesystem_snapshot_policy" "subject" {
  name         = var.policy_name
  description  = "Filesystem snapshot policy data-source integration subject"
  region       = { name = var.policy_region }
  days_of_week = ["mon", "wed"]
  time         = "03:15"
  retention    = 2
  status       = "disabled"
}

data "viettelcloud_fs_filesystem_snapshot_policy" "by_id" {
  id = viettelcloud_fs_filesystem_snapshot_policy.subject.id
}

data "viettelcloud_fs_filesystem_snapshot_policy" "by_name_and_region" {
  name   = viettelcloud_fs_filesystem_snapshot_policy.subject.name
  region = viettelcloud_fs_filesystem_snapshot_policy.subject.region.name
}

data "viettelcloud_fs_filesystem_snapshot_policy" "by_all_filters" {
  id     = upper(viettelcloud_fs_filesystem_snapshot_policy.subject.id)
  name   = " ${viettelcloud_fs_filesystem_snapshot_policy.subject.name} "
  region = " ${viettelcloud_fs_filesystem_snapshot_policy.subject.region.name} "
}
