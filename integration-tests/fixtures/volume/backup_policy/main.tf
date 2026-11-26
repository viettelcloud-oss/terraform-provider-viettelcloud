# Manages the volume chain "backup-policy" of tests/volume_resource.tftest.hcl:
# two backup policies and one volume that attaches one of them. The policies use
# the days_of_month schedule, which needs no first_execution. They sit in the
# region of the volume zone, because the API refuses a policy from another
# region. attached selects the policy of the volume, and null leaves it out.
# create_volume = false removes the volume and keeps the policies. Then a data
# source for each policy reports how many volumes still use it.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "attached" {
  description = "Key of the policy to attach, \"first\" or \"second\". Null attaches none."
  type        = string
  default     = null
}

variable "create_volume" {
  description = "False destroys the volume and keeps both policies."
  type        = bool
  default     = true
}

variable "region" {
  type = string
}

variable "zone" {
  type = string
}

variable "volume_name" {
  type = string
}

variable "volume_type" {
  type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
}

resource "viettelcloud_volume_backup_policy" "policy" {
  for_each = toset(["first", "second"])

  name   = "tf-test-volume-policy-${each.key}"
  region = { name = var.region }
  status = "enabled"

  schedule = {
    execution_strategy = "days_of_month"
    days_of_month      = [1, 15]
    time               = "02:00"
  }
}

resource "viettelcloud_volume" "volume" {
  count = var.create_volume ? 1 : 0

  name = var.volume_name
  size = 30
  zone = { name = var.zone }

  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }

  backup_policy = var.attached == null ? null : { id = viettelcloud_volume_backup_policy.policy[var.attached].id }
}

# Read only after the volume is gone. The id is known from state, so a plan
# reads each policy again and reports the volumes that still use it.
data "viettelcloud_volume_backup_policy" "policy" {
  for_each = { for key in ["first", "second"] : key => key if !var.create_volume }

  id = viettelcloud_volume_backup_policy.policy[each.key].id
}

output "policy_ids" {
  value = { for key, policy in viettelcloud_volume_backup_policy.policy : key => policy.id }
}

output "policy_volume_counts" {
  value = { for key, policy in data.viettelcloud_volume_backup_policy.policy : key => policy.volume_count }
}

output "volume_id" {
  value = one(viettelcloud_volume.volume[*].id)
}

# Every attribute that a later run or the import fixture compares against.
output "volume" {
  value = var.create_volume ? {
    id          = viettelcloud_volume.volume[0].id
    name        = viettelcloud_volume.volume[0].name
    size        = viettelcloud_volume.volume[0].size
    created_at  = viettelcloud_volume.volume[0].created_at
    volume_type = viettelcloud_volume.volume[0].volume_type
  } : null
}
