terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "volume_type" { type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }) }
variable "zone" { type = string }

resource "viettelcloud_volume" "source" {
  name = "tf-test-snapshot-datasource-volume"
  size = 30
  zone = { name = var.zone }
  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }
}

resource "viettelcloud_volume_snapshot" "subject" {
  name        = "tf-test-snapshot-datasource"
  description = "Data source subject"
  volume      = { id = viettelcloud_volume.source.id }
}

data "viettelcloud_volume_snapshot" "by_id" {
  id = viettelcloud_volume_snapshot.subject.id
}

data "viettelcloud_volume_snapshot" "by_name" {
  name = viettelcloud_volume_snapshot.subject.name
}

data "viettelcloud_volume_snapshot" "by_volume" {
  name      = viettelcloud_volume_snapshot.subject.name
  volume_id = viettelcloud_volume.source.id
}

data "viettelcloud_volume_snapshot" "by_zone_and_status" {
  name   = viettelcloud_volume_snapshot.subject.name
  zone   = var.zone
  status = "available"
}

data "viettelcloud_volume_snapshot" "by_all" {
  id        = upper(viettelcloud_volume_snapshot.subject.id)
  name      = " ${viettelcloud_volume_snapshot.subject.name} "
  volume_id = upper(viettelcloud_volume.source.id)
  zone      = " ${var.zone} "
  status    = " available "
}

output "subject" { value = viettelcloud_volume_snapshot.subject }
output "by_id" { value = data.viettelcloud_volume_snapshot.by_id }
output "by_name" { value = data.viettelcloud_volume_snapshot.by_name }
output "by_volume" { value = data.viettelcloud_volume_snapshot.by_volume }
output "by_zone_and_status" { value = data.viettelcloud_volume_snapshot.by_zone_and_status }
output "by_all" { value = data.viettelcloud_volume_snapshot.by_all }
