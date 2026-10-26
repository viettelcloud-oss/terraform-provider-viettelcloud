terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "volumes" {
  type = map(object({
    name        = string
    description = optional(string)
    # Optional because a snapshot source determines the size itself and the
    # provider rejects an explicit size when the volume is created from one.
    size = optional(number)
    iops = optional(number)
    create_from = optional(object({
      source_type  = optional(string, "empty")
      image        = optional(object({ name = optional(string), os = optional(string), version = optional(string), type = optional(string) }))
      custom_image = optional(object({ id = string }))
      snapshot     = optional(object({ id = string }))
      backup       = optional(object({ id = string }))
      # Per-volume override of var.volume_type. The backend retypes in place,
      # so changing it for one fleet member exercises the update path without
      # touching the rest of the fleet.
      volume_type = optional(object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }))
    }), {})
  }))
}

variable "volume_type" {
  type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
}

# Narrows var.volume_type to one zone, because the same criteria can match a
# volume type in another zone.
variable "zone" {
  type    = string
  default = null
}

variable "image_name" {
  type = string
}

resource "viettelcloud_volume" "volume" {
  for_each = var.volumes

  name        = each.value.name
  description = each.value.description
  size        = each.value.size
  iops        = each.value.iops
  # A snapshot determines the new volume's zone.
  zone = (each.value.create_from.source_type == "snapshot" || var.zone == null
    ? null
    : { name = var.zone }
  )

  create_from = {
    source_type  = each.value.create_from.source_type
    image        = each.value.create_from.image
    custom_image = each.value.create_from.custom_image
    snapshot     = each.value.create_from.snapshot
    backup       = each.value.create_from.backup
    # A snapshot carries its own volume type, so the provider rejects the
    # attribute for that source.
    volume_type = (each.value.create_from.source_type == "snapshot"
      ? null
      : (each.value.create_from.volume_type != null ? each.value.create_from.volume_type : var.volume_type)
    )
  }
}

output "volume_ids" {
  value = { for key, volume in viettelcloud_volume.volume : key => volume.id }
}

# Every attribute a later run compares against to prove a plan is empty.
# updated_at is deliberately excluded: the backend may bump it on its own.
output "volumes" {
  value = { for key, volume in viettelcloud_volume.volume : key => {
    id                  = volume.id
    name                = volume.name
    description         = volume.description
    size                = volume.size
    iops                = volume.iops
    status              = volume.status
    bootable            = volume.bootable
    encrypted           = volume.encrypted
    zone                = volume.zone
    zone_id             = volume.zone_id
    volume_type         = volume.volume_type
    project_id          = volume.project_id
    created_at          = volume.created_at
    source_type         = volume.create_from.source_type
    source_image        = volume.create_from.image
    source_custom_image = volume.create_from.custom_image
    source_snapshot     = volume.create_from.snapshot
    source_backup       = volume.create_from.backup
    source_volume_type  = volume.create_from.volume_type
  } }
}
