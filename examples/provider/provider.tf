terraform {
  required_providers {
    viettelcloud = {
      source  = "viettelcloud-oss/viettelcloud"
      version = "~> 0.1.1"
    }
  }
}

provider "viettelcloud" {
  endpoint   = var.viettelcloud_endpoint
  token      = var.viettelcloud_token
  project_id = var.viettelcloud_project_id
}

variable "viettelcloud_endpoint" {
  type = string
}

variable "viettelcloud_token" {
  sensitive = true
  type      = string
}

variable "viettelcloud_project_id" {
  type = string
}
