# Manages one empty volume and two disabled snapshot policies for the
# "snapshot-policy" and "snapshot-policy-import-source" chains of
# tests/volume_resource.tftest.hcl. var.attached selects the policy that the
# volume attaches. A null value leaves snapshot_policy unset. The policies are
# disabled, so no snapshot runs while a chain is active, and teardown can delete
# the volume without snapshots.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "name" {
  type = string
}

variable "size" {
  type = number
}

variable "zone" {
  type = string
}

variable "region" {
  type = string
}

variable "volume_type" {
  type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
}

variable "attached" {
  type    = string
  default = null

  validation {
    condition     = var.attached == null || contains(["first", "second"], coalesce(var.attached, "first"))
    error_message = "attached must be null, first, or second."
  }
}

resource "viettelcloud_volume_snapshot_policy" "policy" {
  for_each = toset(["first", "second"])

  name   = "${var.name}-${each.key}"
  region = { name = var.region }
  status = "disabled"

  schedule = {
    days_of_week = ["mon"]
    time         = "02:00"
  }
}

resource "viettelcloud_volume" "volume" {
  name = var.name
  size = var.size
  zone = { name = var.zone }

  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }

  snapshot_policy = var.attached == null ? null : { id = viettelcloud_volume_snapshot_policy.policy[var.attached].id }
}

output "volume_id" {
  value = viettelcloud_volume.volume.id
}

output "policy_ids" {
  value = { for key, policy in viettelcloud_volume_snapshot_policy.policy : key => policy.id }
}

# Every attribute that run "planAgain_attachedSnapshotPolicy_stateStable" and
# run "importVolume_policyAttachedVolume_snapshotPolicyReturned" compare against
# to prove a plan is empty. updated_at is excluded because the API can change it
# by itself.
output "volume" {
  value = {
    id              = viettelcloud_volume.volume.id
    name            = viettelcloud_volume.volume.name
    size            = viettelcloud_volume.volume.size
    status          = viettelcloud_volume.volume.status
    zone_id         = viettelcloud_volume.volume.zone_id
    volume_type     = viettelcloud_volume.volume.volume_type
    created_at      = viettelcloud_volume.volume.created_at
    snapshot_policy = viettelcloud_volume.volume.snapshot_policy
  }
}
