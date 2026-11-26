# This fixture creates one Load Balancer in an existing VPC and resolves it
# through every supported data-source selector. The Load Balancer owns a name
# that no other Load Balancer in the project uses, so each name lookup selects
# exactly one object.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "vpc_id" {
  type = string
}

variable "name" {
  type = string
}

variable "region" {
  type = string
}

variable "package" {
  type = string
}

variable "topology" {
  type = string
}

# The type lookup needs a known type at plan time. The first apply defers the
# by_id read until the subject exists, so a caller enables the type lookup only
# after the subject is in state.
variable "type_lookup_enabled" {
  type    = bool
  default = false
}

resource "viettelcloud_lb_loadbalancer" "subject" {
  name        = var.name
  description = "Load Balancer data-source integration subject"
  package     = var.package
  topology    = var.topology
  region      = { name = var.region }
  vpc         = { id = var.vpc_id }
}

data "viettelcloud_lb_loadbalancer" "by_id" {
  id = viettelcloud_lb_loadbalancer.subject.id
}

data "viettelcloud_lb_loadbalancer" "by_name" {
  name = viettelcloud_lb_loadbalancer.subject.name
}

data "viettelcloud_lb_loadbalancer" "by_name_and_region" {
  name   = viettelcloud_lb_loadbalancer.subject.name
  region = viettelcloud_lb_loadbalancer.subject.region.name
}

data "viettelcloud_lb_loadbalancer" "by_id_and_name" {
  id   = viettelcloud_lb_loadbalancer.subject.id
  name = viettelcloud_lb_loadbalancer.subject.name
}

data "viettelcloud_lb_loadbalancer" "by_id_and_region" {
  id     = viettelcloud_lb_loadbalancer.subject.id
  region = viettelcloud_lb_loadbalancer.subject.region.name
}

data "viettelcloud_lb_loadbalancer" "by_vip_address" {
  id          = viettelcloud_lb_loadbalancer.subject.id
  vip_address = data.viettelcloud_lb_loadbalancer.by_id.vip_address
}

data "viettelcloud_lb_loadbalancer" "by_package" {
  id      = viettelcloud_lb_loadbalancer.subject.id
  package = viettelcloud_lb_loadbalancer.subject.package
}

data "viettelcloud_lb_loadbalancer" "by_topology" {
  id       = viettelcloud_lb_loadbalancer.subject.id
  topology = viettelcloud_lb_loadbalancer.subject.topology
}

# The backend can omit the type, and the data source then reports null. A null
# type cannot be a filter, so this lookup exists only when type_lookup_enabled
# is true and the backend reports a type.
data "viettelcloud_lb_loadbalancer" "by_type" {
  count = var.type_lookup_enabled ? (data.viettelcloud_lb_loadbalancer.by_id.type == null ? 0 : 1) : 0

  id   = viettelcloud_lb_loadbalancer.subject.id
  type = data.viettelcloud_lb_loadbalancer.by_id.type
}

data "viettelcloud_lb_loadbalancer" "by_access_log" {
  id                 = viettelcloud_lb_loadbalancer.subject.id
  access_log_enabled = data.viettelcloud_lb_loadbalancer.by_id.access_log_enabled
}

# Every criterion in the spelling a practitioner might write: an upper-cased ID
# and padded strings. The type is null, and so unfiltered, when the backend
# omits it.
data "viettelcloud_lb_loadbalancer" "by_all_filters" {
  id                 = upper(viettelcloud_lb_loadbalancer.subject.id)
  name               = " ${viettelcloud_lb_loadbalancer.subject.name} "
  region             = " ${viettelcloud_lb_loadbalancer.subject.region.name} "
  vip_address        = " ${data.viettelcloud_lb_loadbalancer.by_id.vip_address} "
  package            = viettelcloud_lb_loadbalancer.subject.package
  topology           = viettelcloud_lb_loadbalancer.subject.topology
  type               = data.viettelcloud_lb_loadbalancer.by_id.type
  access_log_enabled = data.viettelcloud_lb_loadbalancer.by_id.access_log_enabled
}
