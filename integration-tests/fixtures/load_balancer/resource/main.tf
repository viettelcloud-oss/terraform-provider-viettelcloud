# This fixture owns the Load Balancer of a resource chain.
# It uses an existing VPC that the test file names in vpc_id.

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

variable "vpc_id" {
  type = string
}

variable "name" {
  type = string
}

variable "description" {
  type    = string
  default = null
}

variable "package" {
  type = string
}

variable "topology" {
  type = string
}

variable "type" {
  type    = string
  default = null
}

variable "public_ip_mode" {
  type    = string
  default = null
}

resource "viettelcloud_lb_loadbalancer" "load_balancer" {
  name           = var.name
  description    = var.description
  package        = var.package
  topology       = var.topology
  type           = var.type
  public_ip_mode = var.public_ip_mode
  region         = { name = var.region }
  vpc            = { id = var.vpc_id }
}

output "load_balancer_id" {
  value = viettelcloud_lb_loadbalancer.load_balancer.id
}

output "load_balancer_vpc_id" {
  value = viettelcloud_lb_loadbalancer.load_balancer.vpc.id
}

output "load_balancer_name" {
  value = viettelcloud_lb_loadbalancer.load_balancer.name
}

output "load_balancer_package" {
  value = viettelcloud_lb_loadbalancer.load_balancer.package
}

output "load_balancer_topology" {
  value = viettelcloud_lb_loadbalancer.load_balancer.topology
}

output "load_balancer_region" {
  value = viettelcloud_lb_loadbalancer.load_balancer.region.name
}

output "load_balancer_public_ip_mode" {
  value = viettelcloud_lb_loadbalancer.load_balancer.public_ip_mode
}

output "load_balancer_public_address" {
  value = viettelcloud_lb_loadbalancer.load_balancer.public_address
}

output "load_balancer_updated_at" {
  value = viettelcloud_lb_loadbalancer.load_balancer.updated_at
}
