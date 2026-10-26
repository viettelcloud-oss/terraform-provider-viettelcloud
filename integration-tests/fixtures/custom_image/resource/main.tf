# This fixture manages one volume-backed custom image.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}
variable "name" {
  type = string
}
variable "volume" {
  type    = string
  default = null
}
variable "volume_id" {
  type    = string
  default = null
}
variable "disk_format" {
  type    = string
  default = "qcow2"
}
variable "container_format" {
  type    = string
  default = null
}
variable "description" {
  type    = string
  default = null
}
variable "os" {
  type    = string
  default = null
}
variable "image_version" {
  type    = string
  default = null
}
variable "state" {
  type    = string
  default = null
}
variable "metadata" {
  type    = string
  default = null
}
variable "shared_project_ids" {
  type    = set(string)
  default = null
}
resource "viettelcloud_custom_image" "image" {
  name = var.name
  volume = {
    name = var.volume
    id   = var.volume_id
  }
  disk_format      = var.disk_format
  container_format = var.container_format
  description      = var.description
  os               = var.os
  version          = var.image_version
  state            = var.state
  metadata         = var.metadata
  shared_projects  = var.shared_project_ids == null ? null : [for id in var.shared_project_ids : { id = id }]
}
output "image" { value = viettelcloud_custom_image.image }

# image_as_data_source flattens the image references into the attribute shape
# of the viettelcloud_custom_image data source, so a lookup can be compared
# with the managed image attribute by attribute. sort types the project UUIDs
# as strings: an empty for expression has no element type, so it would never
# equal the empty set of strings that the data source returns.
output "image_as_data_source" {
  value = merge(
    { for k, v in viettelcloud_custom_image.image : k => v if !contains(["volume", "shared_projects"], k) },
    {
      volume             = viettelcloud_custom_image.image.volume == null ? null : viettelcloud_custom_image.image.volume.name
      volume_id          = viettelcloud_custom_image.image.volume == null ? null : viettelcloud_custom_image.image.volume.id
      shared_project_ids = viettelcloud_custom_image.image.shared_projects == null ? null : toset(sort([for project in viettelcloud_custom_image.image.shared_projects : project.id]))
    },
  )
}
