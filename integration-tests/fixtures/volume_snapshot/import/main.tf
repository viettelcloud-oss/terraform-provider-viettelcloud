# This module plans an import while the creating test state owns the snapshot.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "id" { type = string }
variable "name" { type = string }
variable "volume_id" { type = string }

resource "viettelcloud_volume_snapshot" "imported" {
  name   = var.name
  volume = { id = var.volume_id }
}

import {
  to = viettelcloud_volume_snapshot.imported
  id = var.id
}

output "snapshot" { value = viettelcloud_volume_snapshot.imported }
