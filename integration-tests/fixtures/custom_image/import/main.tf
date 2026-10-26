# This fixture manages one volume-backed custom image.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}
variable "image_id" { type = string }
variable "name" { type = string }
variable "volume" {
  type    = string
  default = null
}
variable "volume_id" {
  type    = string
  default = null
}
variable "disk_format" { type = string }
variable "container_format" { type = string }
import {
  to = viettelcloud_custom_image.image
  id = var.image_id
}
resource "viettelcloud_custom_image" "image" {
  name = var.name
  volume = {
    name = var.volume
    id   = var.volume_id
  }
  disk_format      = var.disk_format
  container_format = var.container_format
}
output "image" { value = viettelcloud_custom_image.image }
