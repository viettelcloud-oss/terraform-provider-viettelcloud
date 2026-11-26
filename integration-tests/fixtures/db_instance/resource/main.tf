# This fixture owns one DB instance for a lifecycle test chain. Optional
# inputs default to null, so a run that omits them leaves them unconfigured.
terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "name" { type = string }
variable "region" { type = string }
variable "database_type" { type = string }
variable "db_version" { type = string }
variable "flavor" { type = string }
variable "volume_type" { type = string }
variable "disk_size" { type = number }
variable "solution" { type = string }
variable "subnet_id" { type = string }

variable "root_password" {
  type      = string
  sensitive = true
}

variable "description" {
  type    = string
  default = null
}

variable "keep_backup" {
  type    = bool
  default = null
}

variable "backup_policy" {
  type = object({
    enabled         = bool
    backup_interval = optional(string)
    day_of_week     = optional(string)
    start_hour      = optional(number)
    max_auto_backup = optional(number)
  })
  default = null
}

variable "enable_public_access" {
  type    = bool
  default = null
}

variable "auto_increase_volume_size" {
  type = object({
    enabled   = bool
    threshold = optional(number)
    limit     = optional(number)
  })
  default = null
}

resource "viettelcloud_db_instance" "instance" {
  name                      = var.name
  description               = var.description
  database_type             = var.database_type
  db_version                = { version = var.db_version }
  region                    = { name = var.region }
  flavor                    = { name = var.flavor }
  volume_type               = { name = var.volume_type }
  disk_size                 = var.disk_size
  solution                  = var.solution
  subnet                    = { id = var.subnet_id }
  root_password             = var.root_password
  enable_public_access      = var.enable_public_access
  keep_backup               = var.keep_backup
  backup_policy             = var.backup_policy
  auto_increase_volume_size = var.auto_increase_volume_size
}

# The instance object holds the write-only, sensitive root_password.
output "instance" {
  value     = viettelcloud_db_instance.instance
  sensitive = true
}
