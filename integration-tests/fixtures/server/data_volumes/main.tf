# This fixture uses the Server suite VPC and owns its existing source volumes.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "vpc_id" { type = string }
variable "zone" { type = string }
variable "image" { type = string }
variable "volume_type" { type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) }) }
variable "predefined_flavor_name" { type = string }
variable "server_name" {
  type    = string
  default = "tf-test-server-data-volumes"
}
variable "include_new" {
  type    = bool
  default = true
}
variable "new_kind" {
  type    = string
  default = "new"
}
variable "new_volume_size" {
  type    = number
  default = 20
}
variable "new_delete_on_termination" {
  type    = bool
  default = null
}
variable "existing_volume" {
  type    = string
  default = "primary"
}
variable "include_second_existing" {
  type    = bool
  default = false
}
variable "reverse_order" {
  type    = bool
  default = false
}
variable "omit_volumes" {
  type    = bool
  default = false
}
variable "kept_volume_id" {
  type    = string
  default = null
}

resource "viettelcloud_subnet" "network" {
  name = "tf-test-server-data-volume-subnet"
  cidr = "10.36.251.0/24"
  vpc  = { id = var.vpc_id }
}
resource "viettelcloud_volume" "existing" {
  for_each = toset(["primary", "replacement"])
  name     = "tf-test-server-data-volume-${each.key}"
  size     = 20
  zone     = { name = var.zone }
  create_from = {
    source_type = "empty"
    volume_type = var.volume_type
  }
}

locals {
  new_volumes = var.include_new ? [{
    kind                  = var.new_kind
    id                    = null
    volume_type           = var.volume_type
    volume_size           = var.new_volume_size
    iops                  = null
    delete_on_termination = var.new_delete_on_termination
  }] : []
  existing_keys = var.existing_volume == null ? [] : (var.include_second_existing ? ["primary", "replacement"] : [var.existing_volume])
  existing_volumes = [for key in local.existing_keys : {
    kind                  = "existing"
    id                    = viettelcloud_volume.existing[key].id
    volume_type           = false ? var.volume_type : null # typed null, so that concat can unify the lists
    volume_size           = null
    iops                  = null
    delete_on_termination = null
  }]
  volumes = concat(local.new_volumes, local.existing_volumes)
}

resource "viettelcloud_server" "test" {
  name   = var.server_name
  zone   = { name = var.zone }
  flavor = { kind = "predefined", name = var.predefined_flavor_name }
  boot = {
    boot_type   = "image"
    image       = { name = var.image }
    volume_type = var.volume_type
    volume_size = 30
  }
  private_ips  = [{ kind = "subnet", subnet = { id = viettelcloud_subnet.network.id } }]
  data_volumes = var.omit_volumes ? null : (var.reverse_order ? reverse(local.volumes) : local.volumes)
}

data "viettelcloud_volume" "existing" {
  for_each   = viettelcloud_volume.existing
  id         = each.value.id
  depends_on = [viettelcloud_server.test]
}
data "viettelcloud_volume" "kept" {
  count      = var.kept_volume_id == null ? 0 : 1
  id         = var.kept_volume_id
  depends_on = [viettelcloud_server.test]
}

output "server" {
  value = {
    id              = viettelcloud_server.test.id
    name            = viettelcloud_server.test.name
    data_volumes    = viettelcloud_server.test.data_volumes
    data_volume_ids = viettelcloud_server.test.data_volume_ids
  }
}
output "volumes" {
  value = data.viettelcloud_volume.existing
}
output "kept_volume" {
  value = try(data.viettelcloud_volume.kept[0], null)
}
