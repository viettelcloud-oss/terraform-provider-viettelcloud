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

variable "vpc_name" {
  type = string
}

variable "vpc_cidr" {
  type = string
}

variable "subnet_name" {
  type = string
}

variable "subnet_cidr" {
  type = string
}

variable "private_ip_address" {
  type = string
}

resource "viettelcloud_vpc" "test" {
  name   = var.vpc_name
  cidr   = var.vpc_cidr
  region = { name = var.region }
}

resource "viettelcloud_subnet" "test" {
  name = var.subnet_name
  cidr = var.subnet_cidr
  vpc  = { id = viettelcloud_vpc.test.id }
}

resource "viettelcloud_private_ip" "vip" {
  subnet      = { id = viettelcloud_subnet.test.id }
  description = "Allowed VIP for Private IP data source integration tests"
}

resource "viettelcloud_private_ip" "target" {
  subnet       = { id = viettelcloud_subnet.test.id }
  description  = "Private IP for data source integration tests"
  ip_address   = var.private_ip_address
  allowed_vips = [{ id = viettelcloud_private_ip.vip.id }]
}

data "viettelcloud_private_ip" "by_id" {
  id = viettelcloud_private_ip.target.id
}

data "viettelcloud_vpc" "parent" {
  id = viettelcloud_vpc.test.id
}

data "viettelcloud_private_ip" "by_subnet_id" {
  ip_address = data.viettelcloud_private_ip.by_id.ip_address
  subnet_id  = data.viettelcloud_private_ip.by_id.subnet_id
}

data "viettelcloud_private_ip" "by_parent_ids" {
  ip_address = data.viettelcloud_private_ip.by_id.ip_address
  vpc_id     = data.viettelcloud_private_ip.by_id.vpc_id
  subnet_id  = data.viettelcloud_private_ip.by_id.subnet_id
}

data "viettelcloud_private_ip" "by_names" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  vpc_name    = data.viettelcloud_private_ip.by_id.vpc_name
  subnet_name = data.viettelcloud_private_ip.by_id.subnet_name
}

data "viettelcloud_private_ip" "by_vpc_id_and_subnet_name" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  vpc_id      = data.viettelcloud_private_ip.by_id.vpc_id
  subnet_name = data.viettelcloud_private_ip.by_id.subnet_name
}

data "viettelcloud_private_ip" "by_subnet_name_and_region" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  subnet_name = data.viettelcloud_private_ip.by_id.subnet_name
  region      = data.viettelcloud_private_ip.by_id.region
}

data "viettelcloud_private_ip" "by_subnet_cidr_and_region" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  subnet_cidr = data.viettelcloud_private_ip.by_id.subnet_cidr
  region      = data.viettelcloud_private_ip.by_id.region
}

data "viettelcloud_private_ip" "by_vpc_name_and_region" {
  ip_address = data.viettelcloud_private_ip.by_id.ip_address
  vpc_name   = data.viettelcloud_private_ip.by_id.vpc_name
  region     = data.viettelcloud_private_ip.by_id.region
}

data "viettelcloud_private_ip" "by_vpc_name_and_subnet_cidr" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  vpc_name    = data.viettelcloud_private_ip.by_id.vpc_name
  subnet_cidr = data.viettelcloud_private_ip.by_id.subnet_cidr
}

data "viettelcloud_private_ip" "by_vpc_cidr_and_subnet_name" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  vpc_cidr    = data.viettelcloud_vpc.parent.cidr
  subnet_name = data.viettelcloud_private_ip.by_id.subnet_name
}

data "viettelcloud_private_ip" "by_hierarchy" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  vpc_name    = data.viettelcloud_private_ip.by_id.vpc_name
  vpc_cidr    = data.viettelcloud_vpc.parent.cidr
  subnet_name = data.viettelcloud_private_ip.by_id.subnet_name
  subnet_cidr = data.viettelcloud_private_ip.by_id.subnet_cidr
  region      = data.viettelcloud_private_ip.by_id.region
}

data "viettelcloud_private_ip" "by_vpc_and_subnet_cidrs" {
  ip_address  = data.viettelcloud_private_ip.by_id.ip_address
  vpc_cidr    = data.viettelcloud_vpc.parent.cidr
  subnet_cidr = data.viettelcloud_private_ip.by_id.subnet_cidr
}

# Supplying every criterion exercises the list path and configured-value
# preservation with semantically equivalent UUID and text representations.
data "viettelcloud_private_ip" "by_all_filters" {
  id          = upper(data.viettelcloud_private_ip.by_id.id)
  ip_address  = " ${data.viettelcloud_private_ip.by_id.ip_address} "
  subnet_id   = upper(data.viettelcloud_private_ip.by_id.subnet_id)
  subnet_name = " ${data.viettelcloud_private_ip.by_id.subnet_name} "
  subnet_cidr = " ${data.viettelcloud_private_ip.by_id.subnet_cidr} "
  vpc_id      = upper(data.viettelcloud_private_ip.by_id.vpc_id)
  vpc_name    = " ${data.viettelcloud_private_ip.by_id.vpc_name} "
  vpc_cidr    = " ${data.viettelcloud_vpc.parent.cidr} "
  region      = " ${data.viettelcloud_private_ip.by_id.region} "
}

output "private_ip_id" {
  value = viettelcloud_private_ip.target.id
}
