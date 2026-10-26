# This fixture manages one volume-backed custom image.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}
variable "id" {
  type    = string
  default = null
}
variable "name" {
  type    = string
  default = null
}
variable "volume" {
  type    = string
  default = null
}
variable "volume_id" {
  type    = string
  default = null
}
variable "region" {
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
variable "status" {
  type    = string
  default = null
}
data "viettelcloud_custom_image" "image" {
  id        = var.id
  name      = var.name
  volume    = var.volume
  volume_id = var.volume_id
  region    = var.region
  os        = var.os
  version   = var.image_version
  state     = var.state
  status    = var.status
}
output "image" { value = data.viettelcloud_custom_image.image }
