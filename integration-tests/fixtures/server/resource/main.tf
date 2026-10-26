terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "region" {
  type = string
}

variable "zone" {
  type = string
}

# Names every resource this module creates. Each parallel chain in the test file
# passes its own prefix so the chains never share a name.
variable "name_prefix" {
  type = string
}

# The VPC every chain shares. The test file creates it once, because the backend
# allocates a route table per VPC and rejects creating several at once.
variable "vpc_id" {
  type = string
}

# First two octets of the shared VPC's address space, for example "10.36".
variable "vpc_cidr_prefix" {
  type    = string
  default = "10.36"
}

# Third octet of the /24 this module carves out of the shared VPC. Each chain
# passes its own index, and the second subnet takes the next octet up, so no two
# chains overlap.
variable "subnet_index" {
  type = number
}

# Which subnets the server allocates a private IP from: first, second, or both.
# The second subnet can outlive its attachment so detach and subnet deletion
# happen in separate applies.
# Plan-only omission coverage keeps all existing references and attachments.
variable "omit_preserved_references" {
  type    = bool
  default = false
}

variable "private_ip_mode" {
  type    = string
  default = "first"
}

# Reverses the configured attachment order without changing the attachment set.
variable "reverse_private_ips" {
  type    = bool
  default = false
}

variable "keep_second_subnet" {
  type    = bool
  default = false
}

# Creates a private IP managed independently of the server.
variable "create_managed_private_ip" {
  type    = bool
  default = false
}

# Attaches the independently managed private IP to the server by ID.
variable "attach_managed_private_ip" {
  type    = bool
  default = false
}

# Left unset unless a run exercises the name itself.
variable "server_name" {
  type    = string
  default = null
}

variable "server_description" {
  type    = string
  default = null
}

variable "image" {
  type = string
}

variable "volume_type" {
  type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
}

# Left unset so the backend allocates the boot volume IOPS.
variable "boot_iops" {
  type    = number
  default = null
}

variable "boot_volume_size" {
  type    = number
  default = 30
}

variable "bandwidth" {
  type    = number
  default = null
}

variable "flavor_kind" {
  type    = string
  default = "predefined"
}

variable "predefined_flavor_name" {
  type    = string
  default = "b1.micro2x"
}

# The backend refuses a resize whose flavor resolves to the one the server
# already runs on, so these defaults must not describe the same capacity as
# `predefined_flavor_name`. `b1.micro2x` is 2 vCPU / 2 GB.
variable "flavor_vcpus" {
  type    = number
  default = 2
}

variable "flavor_ram" {
  type    = number
  default = 4
}

variable "include_data_volume" {
  type    = bool
  default = true
}

variable "data_volume_id" {
  type    = string
  default = null
}

variable "second_data_volume_id" {
  type    = string
  default = null
}

variable "data_volume_size" {
  type    = number
  default = 20
}

# The second data volume is opt-in because most chains only need one. It proves
# data_volumes[i].id binds each element to its own backend volume, which a
# single-volume list cannot show.
variable "second_data_volume_size" {
  type    = number
  default = null
}

# Left unset so a run that omits them exercises the provider default: the boot
# volume and the data volume are deleted with the server.
variable "boot_delete_on_termination" {
  type    = bool
  default = null
}

variable "data_volume_delete_on_termination" {
  type    = bool
  default = null
}

variable "elastic_ip_count" {
  type    = number
  default = 0
}

variable "power_state" {
  type    = string
  default = null
}

variable "user_data" {
  type    = string
  default = null
}

variable "key_pair_id" {
  type    = string
  default = null
}

variable "placement_group_id" {
  type    = string
  default = null
}

variable "security_group_ids" {
  type    = set(string)
  default = null
}

resource "viettelcloud_subnet" "subnet" {
  name = "${var.name_prefix}-subnet"
  cidr = "${var.vpc_cidr_prefix}.${var.subnet_index}.0/24"
  vpc  = { id = var.vpc_id }
}

resource "viettelcloud_subnet" "second" {
  count = local.needs_second_subnet ? 1 : 0

  name = "${var.name_prefix}-subnet-second"
  cidr = "${var.vpc_cidr_prefix}.${var.subnet_index + 1}.0/24"
  vpc  = { id = var.vpc_id }
}

# A private IP the practitioner owns. The server may attach it by ID, and
# detaching it must never delete it.
resource "viettelcloud_private_ip" "managed" {
  count = var.create_managed_private_ip ? 1 : 0

  subnet      = { id = viettelcloud_subnet.subnet.id }
  description = "Private IP managed independently and attached to the server by ID"
}

locals {
  needs_second_subnet = var.keep_second_subnet || contains(["second", "both"], var.private_ip_mode)

  subnet_ids = concat(
    contains(["first", "both"], var.private_ip_mode) ? [viettelcloud_subnet.subnet.id] : [],
    contains(["second", "both"], var.private_ip_mode) ? viettelcloud_subnet.second[*].id : [],
  )

  subnet_private_ips = [for subnet_id in local.subnet_ids : {
    kind   = "subnet"
    id     = null
    subnet = { id = subnet_id, cidr = null }
  }]

  managed_private_ips = var.attach_managed_private_ip ? [{
    kind                  = "ip"
    id                    = viettelcloud_private_ip.managed[0].id
    subnet                = null
    delete_on_termination = false
  }] : []

  flavor = var.flavor_kind == "predefined" ? {
    kind   = "predefined"
    name   = var.predefined_flavor_name
    family = null
    vcpus  = null
    ram    = null
    } : {
    kind   = "custom"
    name   = null
    family = "basic"
    vcpus  = var.flavor_vcpus
    ram    = var.flavor_ram
  }
}

resource "viettelcloud_server" "server" {
  name            = var.server_name == null ? "${var.name_prefix}-server" : var.server_name
  description     = var.server_description
  zone            = var.omit_preserved_references ? null : { name = var.zone }
  bandwidth       = var.bandwidth
  power_state     = var.power_state
  user_data       = var.user_data
  key_pair        = var.omit_preserved_references || var.key_pair_id == null ? null : { id = var.key_pair_id }
  placement_group = var.omit_preserved_references || var.placement_group_id == null ? null : { id = var.placement_group_id }
  security_groups = var.omit_preserved_references || var.security_group_ids == null ? null : [for id in var.security_group_ids : { id = id }]

  flavor = local.flavor

  boot = {
    boot_type             = "image"
    image                 = var.image_selector != null ? var.image_selector : { name = var.image }
    volume_type           = var.volume_type_selector != null ? var.volume_type_selector : var.volume_type
    volume_size           = var.boot_volume_size
    iops                  = var.boot_iops
    delete_on_termination = var.boot_delete_on_termination
  }

  data_volumes = concat(
    var.include_data_volume ? [{
      id                    = var.data_volume_id
      volume_type           = var.data_volume_type_selector != null ? var.data_volume_type_selector : (var.volume_type_selector != null ? var.volume_type_selector : var.volume_type)
      volume_size           = var.data_volume_size
      delete_on_termination = var.data_volume_delete_on_termination
    }] : [],
    var.second_data_volume_size == null ? [] : [{
      id                    = var.second_data_volume_id
      volume_type           = var.data_volume_type_selector != null ? var.data_volume_type_selector : (var.volume_type_selector != null ? var.volume_type_selector : var.volume_type)
      volume_size           = var.second_data_volume_size
      delete_on_termination = var.data_volume_delete_on_termination
    }],
  )

  private_ips = var.omit_preserved_references ? null : concat(
    var.reverse_private_ips ? reverse(local.subnet_private_ips) : local.subnet_private_ips,
    local.managed_private_ips,
  )

  elastic_ips = var.omit_preserved_references ? null : [for index in range(var.elastic_ip_count) : {
    kind        = "new"
    enable_ipv4 = true
  }]
}

output "server_id" {
  value = viettelcloud_server.server.id
}

output "key_pair_id" {
  value = try(viettelcloud_server.server.key_pair.id, null)
}

output "key_pair_name" {
  value = viettelcloud_server.server.key_pair_name
}

output "placement_group_id" {
  value = try(viettelcloud_server.server.placement_group.id, null)
}

output "placement_group_name" {
  value = viettelcloud_server.server.placement_group_name
}

output "security_group_ids" {
  value = toset(viettelcloud_server.server.security_groups[*].id)
}

output "private_ip_id" {
  value = viettelcloud_server.server.private_ips[0].id
}

output "private_ip_ids" {
  value = viettelcloud_server.server.private_ips[*].id
}

output "elastic_ip_ids" {
  value = viettelcloud_server.server.elastic_ips[*].id
}

output "managed_private_ip_id" {
  value = one(viettelcloud_private_ip.managed[*].id)
}

output "data_volume_ids" {
  value = viettelcloud_server.server.data_volume_ids
}

# Per-element IDs follow data_volumes order, while data_volume_ids follows the
# order the server reported. A later run compares against this one to show the
# element kept the volume it was created with.
output "data_volume_element_ids" {
  value = viettelcloud_server.server.data_volumes[*].id
}

output "server_references" {
  value = {
    zone            = viettelcloud_server.server.zone
    key_pair        = viettelcloud_server.server.key_pair
    placement_group = viettelcloud_server.server.placement_group
    security_groups = viettelcloud_server.server.security_groups
    private_ips     = viettelcloud_server.server.private_ips
    elastic_ips     = viettelcloud_server.server.elastic_ips
    status          = viettelcloud_server.server.status
  }
}

variable "image_selector" {
  type    = object({ name = optional(string), os = optional(string), version = optional(string), type = optional(string) })
  default = null
}

variable "volume_type_selector" {
  type    = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
  default = null
}

variable "data_volume_type_selector" {
  type    = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
  default = null
}

output "server_created_at" {
  value = viettelcloud_server.server.created_at
}

output "data_volume_selectors" {
  value = viettelcloud_server.server.data_volumes[*].volume_type
}

output "data_volume_state" {
  value = viettelcloud_server.server.data_volumes
}
