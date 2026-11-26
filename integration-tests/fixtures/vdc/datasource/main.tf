# This fixture reads an existing vDC of the test project through every
# supported data-source selector. It creates no infrastructure, because only
# an operator can create a vDC.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "vdc_id" {
  type = string
}

data "viettelcloud_vdc" "by_id" {
  id = var.vdc_id
}

data "viettelcloud_vdc" "by_name_and_zone" {
  name = data.viettelcloud_vdc.by_id.name
  zone = data.viettelcloud_vdc.by_id.zone
}

data "viettelcloud_vdc" "by_name_and_region" {
  name   = data.viettelcloud_vdc.by_id.name
  region = data.viettelcloud_vdc.by_id.region
}

data "viettelcloud_vdc" "untrimmed" {
  id     = upper(data.viettelcloud_vdc.by_id.id)
  name   = " ${data.viettelcloud_vdc.by_id.name} "
  region = " ${data.viettelcloud_vdc.by_id.region} "
  zone   = " ${data.viettelcloud_vdc.by_id.zone} "
}
