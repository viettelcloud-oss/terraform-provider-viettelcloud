# This fixture manages one Server Group Member. Weight and backup default to
# null so that runs can omit them and observe the provider defaults.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "server_group_id" { type = string }
variable "name" { type = string }
variable "address" { type = string }
variable "protocol_port" { type = number }
variable "weight" {
  type    = number
  default = null
}
variable "backup" {
  type    = bool
  default = null
}

resource "viettelcloud_lb_member" "subject" {
  server_group  = { id = var.server_group_id }
  name          = var.name
  address       = var.address
  protocol_port = var.protocol_port
  weight        = var.weight
  backup        = var.backup
}

output "member" { value = viettelcloud_lb_member.subject }
