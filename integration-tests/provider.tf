# Terraform 1.16 supports import blocks in modules used by the test runs.
terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "token" {
  description = "Viettel Cloud API access token used by the integration tests."
  type        = string
  sensitive   = true
}

variable "project_id" {
  description = "Project UUID or slug used by the integration tests."
  type        = string
}

variable "api_endpoint" {
  description = "Viettel Cloud API endpoint used by the integration tests."
  type        = string
}

variable "keypair_id" {
  description = "Key pair UUID used by server integration tests."
  type        = string
  default     = null
}

variable "placementgroup_id" {
  description = "Placement group UUID used by server integration tests."
  type        = string
  default     = null
}

variable "security_group_id" {
  description = "Security group UUID used by server integration tests."
  type        = string
  default     = null
}
