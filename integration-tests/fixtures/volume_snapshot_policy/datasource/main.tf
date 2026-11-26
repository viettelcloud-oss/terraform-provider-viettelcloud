# This fixture creates one volume snapshot policy and resolves it in four ways.
# It uses the direct ID path, the name and region criteria, the name, region,
# and status criteria, and every criterion in a non-canonical spelling.
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

resource "viettelcloud_volume_snapshot_policy" "subject" {
  name        = var.policy_name
  description = "Volume snapshot policy data-source integration subject"
  region      = { name = var.policy_region }
  status      = "disabled"
  retention   = 2

  schedule = {
    days_of_week = ["mon", "wed"]
    time         = "03:15"
  }
}

data "viettelcloud_volume_snapshot_policy" "by_id" {
  id = viettelcloud_volume_snapshot_policy.subject.id
}

data "viettelcloud_volume_snapshot_policy" "by_name_and_region" {
  name   = viettelcloud_volume_snapshot_policy.subject.name
  region = viettelcloud_volume_snapshot_policy.subject.region.name
}

data "viettelcloud_volume_snapshot_policy" "by_name_region_and_status" {
  name   = viettelcloud_volume_snapshot_policy.subject.name
  region = viettelcloud_volume_snapshot_policy.subject.region.name
  status = viettelcloud_volume_snapshot_policy.subject.status
}

data "viettelcloud_volume_snapshot_policy" "by_all_filters" {
  id     = upper(viettelcloud_volume_snapshot_policy.subject.id)
  name   = " ${viettelcloud_volume_snapshot_policy.subject.name} "
  region = " ${viettelcloud_volume_snapshot_policy.subject.region.name} "
  status = " ${viettelcloud_volume_snapshot_policy.subject.status} "
}
