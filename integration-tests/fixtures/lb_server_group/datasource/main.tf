# This fixture owns its lookup subject and reads it through all public selectors.
# Reads use canonical values so that full response comparisons remain meaningful.

terraform {
  required_version = ">= 1.16"
  required_providers {
    viettelcloud = { source = "viettelcloud-oss/viettelcloud" }
  }
}

variable "load_balancer_id" { type = string }

resource "viettelcloud_lb_server_group" "subject" {
  load_balancer = { id = var.load_balancer_id }
  name          = "tf-int-server-group-datasource"
  protocol      = "HTTP"
  algorithm     = "ROUND_ROBIN"
  description   = "Data source subject"
  enable_h2     = false
  session_persistence = {
    type        = "APP_COOKIE"
    cookie_name = "SESSIONID"
  }
  health_check = {
    monitor_type     = "HTTP"
    delay            = 10
    timeout          = 5
    max_retries      = 3
    max_retries_down = 3
    path             = "/health"
    http_method      = "GET"
    expected_codes   = "200"
  }
}

data "viettelcloud_lb_server_group" "by_id" {
  load_balancer_id = var.load_balancer_id
  id               = viettelcloud_lb_server_group.subject.id
}

data "viettelcloud_lb_server_group" "by_name" {
  load_balancer_id = var.load_balancer_id
  name             = viettelcloud_lb_server_group.subject.name
}

data "viettelcloud_lb_server_group" "by_id_and_name" {
  load_balancer_id = var.load_balancer_id
  id               = viettelcloud_lb_server_group.subject.id
  name             = viettelcloud_lb_server_group.subject.name
}

data "viettelcloud_lb_server_group" "by_normalized" {
  load_balancer_id = var.load_balancer_id
  id               = upper(viettelcloud_lb_server_group.subject.id)
  name             = " ${viettelcloud_lb_server_group.subject.name} "
}

output "subject" { value = viettelcloud_lb_server_group.subject }

locals {
  lookups = {
    by_id          = data.viettelcloud_lb_server_group.by_id
    by_name        = data.viettelcloud_lb_server_group.by_name
    by_id_and_name = data.viettelcloud_lb_server_group.by_id_and_name
  }
}

# The backend can bump updated_at about one second after it creates the subject,
# so full comparisons leave that field out. Tests check it separately.
output "lookup_views" {
  value = { for key, lookup in local.lookups : key => { for name, value in lookup : name => value if name != "updated_at" } }
}

# The data source returns the load balancer as a flat ID, so this output gives
# the subject in the data-source shape for full comparisons. It leaves out
# updated_at for the same reason as lookup_views.
output "subject_lookup_view" {
  value = merge(
    { for name, value in viettelcloud_lb_server_group.subject : name => value if name != "load_balancer" && name != "updated_at" },
    {
      load_balancer_id = viettelcloud_lb_server_group.subject.load_balancer.id
    },
  )
}

output "by_id" { value = data.viettelcloud_lb_server_group.by_id }
output "by_name" { value = data.viettelcloud_lb_server_group.by_name }
output "by_id_and_name" { value = data.viettelcloud_lb_server_group.by_id_and_name }
output "by_normalized" { value = data.viettelcloud_lb_server_group.by_normalized }
