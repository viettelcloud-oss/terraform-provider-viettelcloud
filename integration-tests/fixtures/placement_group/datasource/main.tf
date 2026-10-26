# This fixture creates one Placement Group and resolves it through every
# supported data-source selector.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "placement_group_name" {
  type = string
}

variable "placement_group_policy" {
  type = string
}

variable "placement_group_region" {
  type = string
}

resource "viettelcloud_placement_group" "subject" {
  name        = var.placement_group_name
  description = "Placement Group data-source integration subject"
  policy      = var.placement_group_policy
  region = {
    name = var.placement_group_region
  }
}

data "viettelcloud_placement_group" "by_id" {
  id = viettelcloud_placement_group.subject.id
}

data "viettelcloud_placement_group" "by_name" {
  name = viettelcloud_placement_group.subject.name
}

data "viettelcloud_placement_group" "by_name_and_policy" {
  name   = viettelcloud_placement_group.subject.name
  policy = viettelcloud_placement_group.subject.policy
}

data "viettelcloud_placement_group" "by_name_and_region" {
  name   = viettelcloud_placement_group.subject.name
  region = viettelcloud_placement_group.subject.region.name
}

data "viettelcloud_placement_group" "by_id_and_name" {
  id   = viettelcloud_placement_group.subject.id
  name = viettelcloud_placement_group.subject.name
}

data "viettelcloud_placement_group" "by_all_filters" {
  id     = upper(viettelcloud_placement_group.subject.id)
  name   = " ${viettelcloud_placement_group.subject.name} "
  policy = " ${viettelcloud_placement_group.subject.policy} "
  region = " ${viettelcloud_placement_group.subject.region.name} "
}
