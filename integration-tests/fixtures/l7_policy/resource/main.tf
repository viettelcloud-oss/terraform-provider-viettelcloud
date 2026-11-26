# Fixture for the chains of tests/l7_policy_resource.tftest.hcl. It manages
# one policy per key of var.policies on the existing listener listener_id.
#
# It does not own the listener, its Load Balancer, or the server group that a
# policy redirects to. The suite passes their IDs as variables.

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "listener_id" {
  type = string
}

# One policy per key. An empty map destroys every policy of the chain.
variable "policies" {
  type = map(object({
    name                     = string
    action                   = string
    description              = optional(string)
    position                 = optional(number)
    redirect_server_group_id = optional(string)
    redirect_url             = optional(string)
    redirect_prefix          = optional(string)
    redirect_http_code       = optional(number)
  }))
}

resource "viettelcloud_lb_l7_policy" "policy" {
  for_each = var.policies

  listener              = { id = var.listener_id }
  name                  = each.value.name
  description           = each.value.description
  action                = each.value.action
  position              = each.value.position
  redirect_server_group = each.value.redirect_server_group_id == null ? null : { id = each.value.redirect_server_group_id }
  redirect_url          = each.value.redirect_url
  redirect_prefix       = each.value.redirect_prefix
  redirect_http_code    = each.value.redirect_http_code
}

output "policies" {
  value = viettelcloud_lb_l7_policy.policy
}
