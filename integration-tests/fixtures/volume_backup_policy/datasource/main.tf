# This fixture creates one volume backup policy and resolves it through the
# direct ID path, the name and region criteria, the name, region, and status
# criteria, and every criterion in a non-canonical spelling. The subject uses a
# days_of_month schedule, so the test needs no future timestamp.
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

resource "viettelcloud_volume_backup_policy" "subject" {
  name        = var.policy_name
  description = "Volume backup policy data-source integration subject"
  region      = { name = var.policy_region }
  status      = "disabled"
  retention   = 2
  schedule = {
    execution_strategy = "days_of_month"
    days_of_month      = [3, 17]
    time               = "03:15"
  }
}

data "viettelcloud_volume_backup_policy" "by_id" {
  id = viettelcloud_volume_backup_policy.subject.id
}

data "viettelcloud_volume_backup_policy" "by_name_and_region" {
  name   = viettelcloud_volume_backup_policy.subject.name
  region = viettelcloud_volume_backup_policy.subject.region.name
}

data "viettelcloud_volume_backup_policy" "by_name_region_and_status" {
  name   = viettelcloud_volume_backup_policy.subject.name
  region = viettelcloud_volume_backup_policy.subject.region.name
  status = viettelcloud_volume_backup_policy.subject.status
}

data "viettelcloud_volume_backup_policy" "by_all_filters" {
  id     = upper(viettelcloud_volume_backup_policy.subject.id)
  name   = " ${viettelcloud_volume_backup_policy.subject.name} "
  region = " ${viettelcloud_volume_backup_policy.subject.region.name} "
  status = viettelcloud_volume_backup_policy.subject.status
}
