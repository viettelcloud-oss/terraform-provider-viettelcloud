# Plan-only fixture for the import chain of
# tests/load_balancer_resource.tftest.hcl. It imports the Load Balancer that run
# "createLoadBalancer_importFixture_loadBalancerCreated" created, rather than
# creating one of its own.
#
# Never apply it. That Load Balancer already belongs to state_key "import", so a
# second owner would delete it twice at teardown.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "load_balancer_id" {
  type = string
}

variable "name" {
  type = string
}

variable "package" {
  type = string
}

variable "topology" {
  type = string
}

variable "region" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "public_ip_mode" {
  type = string
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_lb_loadbalancer.imported
  id = var.load_balancer_id
}

# Only the attributes whose omission would plan a change are configured, fed
# from the state of the run that created this Load Balancer. Every remaining
# attribute is refreshed from the backend.
resource "viettelcloud_lb_loadbalancer" "imported" {
  name           = var.name
  package        = var.package
  topology       = var.topology
  public_ip_mode = var.public_ip_mode
  region         = { name = var.region }
  vpc            = { id = var.vpc_id }
}

output "load_balancer_id" {
  value = viettelcloud_lb_loadbalancer.imported.id
}
