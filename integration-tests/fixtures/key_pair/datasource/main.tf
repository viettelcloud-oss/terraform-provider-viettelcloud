terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "key_pair1_name" {
  type    = string
  default = "tf-test-ds-kp-1"
}

variable "key_pair2_name" {
  type    = string
  default = "tf-test-ds-kp-2"
}

# Two key pairs so every filter has to discriminate between candidates.
resource "viettelcloud_key_pair" "kp1" {
  name = var.key_pair1_name
}

resource "viettelcloud_key_pair" "kp2" {
  name = var.key_pair2_name
}

data "viettelcloud_key_pair" "by_id" {
  id = viettelcloud_key_pair.kp1.id
}

data "viettelcloud_key_pair" "by_name" {
  name = viettelcloud_key_pair.kp2.name
}

data "viettelcloud_key_pair" "by_fingerprint" {
  fingerprint = viettelcloud_key_pair.kp1.fingerprint
}

# Untrimmed name, uppercase fingerprint, and upper-case ID must resolve the same key pair and stay in
# state as configured.
data "viettelcloud_key_pair" "by_all_filters" {
  id          = upper(viettelcloud_key_pair.kp1.id)
  name        = " ${var.key_pair1_name} "
  fingerprint = upper(viettelcloud_key_pair.kp1.fingerprint)
}

output "kp1_id" {
  value = viettelcloud_key_pair.kp1.id
}

output "kp2_id" {
  value = viettelcloud_key_pair.kp2.id
}

output "kp1_fingerprint" {
  value = viettelcloud_key_pair.kp1.fingerprint
}
