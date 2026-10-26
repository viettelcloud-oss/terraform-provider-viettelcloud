# This fixture creates one backup and reads it through every supported filter.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "volume_type" { type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }) }
variable "zone" { type = string }

resource "viettelcloud_volume" "source" {
  name = "tf-test-backup-datasource-volume"
  size = 30
  zone = { name = var.zone }
  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }
}

resource "viettelcloud_volume_backup" "subject" {
  name        = "tf-test-backup-datasource"
  description = "lookup subject"
  volume      = { id = viettelcloud_volume.source.id }
}

data "viettelcloud_volume_backup" "by_id" { id = viettelcloud_volume_backup.subject.id }
data "viettelcloud_volume_backup" "by_name" { name = viettelcloud_volume_backup.subject.name }
data "viettelcloud_volume_backup" "by_volume" { volume_id = viettelcloud_volume_backup.subject.volume.id }
data "viettelcloud_volume_backup" "by_status" {
  name   = viettelcloud_volume_backup.subject.name
  status = viettelcloud_volume_backup.subject.status
}
data "viettelcloud_volume_backup" "by_size" {
  name = viettelcloud_volume_backup.subject.name
  size = viettelcloud_volume_backup.subject.size
}
data "viettelcloud_volume_backup" "by_region" {
  name   = viettelcloud_volume_backup.subject.name
  region = viettelcloud_volume_backup.subject.region
}
data "viettelcloud_volume_backup" "combined" {
  id        = upper(viettelcloud_volume_backup.subject.id)
  name      = " ${viettelcloud_volume_backup.subject.name} "
  volume_id = upper(viettelcloud_volume_backup.subject.volume.id)
  status    = " ${viettelcloud_volume_backup.subject.status} "
  size      = viettelcloud_volume_backup.subject.size
  region    = " ${viettelcloud_volume_backup.subject.region} "
}

output "subject" { value = viettelcloud_volume_backup.subject }
output "by_id" { value = data.viettelcloud_volume_backup.by_id }
output "by_name" { value = data.viettelcloud_volume_backup.by_name }
output "by_volume" { value = data.viettelcloud_volume_backup.by_volume }
output "by_status" { value = data.viettelcloud_volume_backup.by_status }
output "by_size" { value = data.viettelcloud_volume_backup.by_size }
output "by_region" { value = data.viettelcloud_volume_backup.by_region }
output "combined" { value = data.viettelcloud_volume_backup.combined }
