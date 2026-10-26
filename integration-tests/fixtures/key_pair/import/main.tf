terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

# This module imports a key pair another run created instead of creating one,
# so it is only ever planned. Applying it would put a key pair another state key
# already owns under two owners, and teardown would then delete it twice.
variable "key_pair_id" {
  type = string
}

variable "key_pair_name" {
  type = string
}

import {
  to = viettelcloud_key_pair.imported
  id = var.key_pair_id
}

resource "viettelcloud_key_pair" "imported" {
  name = var.key_pair_name
}

output "imported_id" {
  value = viettelcloud_key_pair.imported.id
}
