# This fixture creates the available volumes for the backup lifecycle tests.
# The lifecycle and import chains use two backup sources, and the restore chain
# uses one backup source and two restore targets.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "volume_type" { type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }) }
variable "zone" { type = string }

resource "viettelcloud_volume" "source" {
  for_each = toset(["primary", "replacement", "restore_source", "restore_first", "restore_second"])
  name     = "tf-test-backup-source-${each.key}"
  size     = 30
  zone     = { name = var.zone }
  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }
}

output "volume_ids" {
  value = { for key, volume in viettelcloud_volume.source : key => volume.id }
}
