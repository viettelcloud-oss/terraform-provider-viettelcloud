# This fixture owns the Private IP and Load Balancer in the association chain.
# It uses an existing VPC and subnet that the test file names in vpc_id and
# subnet_id.

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

variable "subnet_id" {
  type = string
}

variable "configure_private_ip" {
  type    = bool
  default = true
}

variable "name" {
  type = string
}

resource "viettelcloud_private_ip" "load_balancer" {
  subnet      = { id = var.subnet_id }
  description = "Private IP for Load Balancer UUID normalization tests"
}

resource "viettelcloud_lb_loadbalancer" "load_balancer" {
  name       = var.name
  package    = "MEDIUM"
  topology   = "SINGLE"
  region     = { name = var.region }
  vpc        = { id = var.vpc_id }
  private_ip = var.configure_private_ip ? { id = upper(viettelcloud_private_ip.load_balancer.id) } : null
}

output "load_balancer_id" {
  value = viettelcloud_lb_loadbalancer.load_balancer.id
}

output "configured_private_ip_id" {
  value = upper(viettelcloud_private_ip.load_balancer.id)
}

output "load_balancer_private_ip_id" {
  value = viettelcloud_lb_loadbalancer.load_balancer.private_ip_id
}

output "load_balancer_updated_at" {
  value = viettelcloud_lb_loadbalancer.load_balancer.updated_at
}
