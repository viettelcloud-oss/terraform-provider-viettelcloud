# Plan-only fixture for the import chain of
# tests/server_resource.tftest.hcl. It imports the server that run
# "createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned"
# created, so it declares no subnet, private IP, or volume of its own.
#
# Never apply it. That server already belongs to state_key "import", so a
# second owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "server_id" {
  type = string
}

variable "server_name" {
  type = string
}

variable "region" {
  type = string
}

variable "zone" {
  type = string
}

variable "image" {
  type = string
}

variable "volume_type" {
  type = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
}

variable "boot_volume_size" {
  type    = number
  default = 30
}

variable "data_volume_size" {
  type    = number
  default = 20
}

variable "flavor_kind" {
  type    = string
  default = "predefined"
}

variable "predefined_flavor_name" {
  type = string
}

# Declared because the test file sets them for every run. The imported server
# already carries these values and every one of them is computed, so restating
# them here would only risk a diff the backend, not the configuration, owns.
variable "bandwidth" {
  type    = number
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

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_server.imported
  id = var.server_id
}

# Import keeps conservative ownership. Configure each attached volume by ID.
variable "data_volumes" {
  type = list(object({
    kind                  = optional(string, "existing")
    id                    = string
    delete_on_termination = optional(bool, false)
  }))
}

resource "viettelcloud_server" "imported" {
  name = var.server_name
  zone = { name = var.zone }

  flavor = {
    kind   = var.flavor_kind
    name   = var.predefined_flavor_name
    family = null
    vcpus  = null
    ram    = null
  }

  # Import records only the name and ID of the selected volume type. Other
  # criteria would plan an in-place update and make computed values unknown.
  # Matching criteria keep the imported ID without a catalog lookup, so the
  # ambiguous name is safe here.
  boot = {
    boot_type   = "image"
    image       = { name = var.image }
    volume_type = { name = var.volume_type.name }
    volume_size = var.boot_volume_size
  }

  data_volumes = var.data_volumes
}

output "server_id" {
  value = viettelcloud_server.imported.id
}
