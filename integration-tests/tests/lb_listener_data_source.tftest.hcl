# This lookup chain owns its subject and releases it at teardown.
# Chain lookup (state_key = "lookup").
#   run "readListener_idNameAndCombinedCriteria_completeStateReturned" {

variable "token" {
  type      = string
  sensitive = true
}
variable "project_id" {
  type = string
}
variable "api_endpoint" {
  type = string
}
variable "listener_load_balancer_id" {
  type = string
}
variable "listener_port" {
  type    = number
  default = 18080
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

run "readListener_idNameAndCombinedCriteria_completeStateReturned" {
  command   = apply
  state_key = "lookup"
  module {
    source = "./fixtures/lb_listener/datasource"
  }
  variables {
    load_balancer_id = var.listener_load_balancer_id
    name             = "tf-test-listener-lookup"
    protocol         = "HTTP"
    protocol_port    = var.listener_port + 4
  }
  assert {
    condition = (data.viettelcloud_lb_listener.by_id.id == viettelcloud_lb_listener.listener.id &&
      data.viettelcloud_lb_listener.by_id.load_balancer_id == viettelcloud_lb_listener.listener.load_balancer.id &&
      data.viettelcloud_lb_listener.by_id.protocol == viettelcloud_lb_listener.listener.protocol &&
      data.viettelcloud_lb_listener.by_id.protocol_port == viettelcloud_lb_listener.listener.protocol_port &&
      data.viettelcloud_lb_listener.by_id.server_group_id == try(viettelcloud_lb_listener.listener.server_group.id, null) &&
      data.viettelcloud_lb_listener.by_id.default_tls_cert_id == try(viettelcloud_lb_listener.listener.default_tls_cert.id, null) &&
      data.viettelcloud_lb_listener.by_id.sni_tls_cert_ids == (viettelcloud_lb_listener.listener.sni_tls_certs == null ? null : toset([for cert in viettelcloud_lb_listener.listener.sni_tls_certs : cert.id])) &&
      data.viettelcloud_lb_listener.by_id.allowed_cidrs == viettelcloud_lb_listener.listener.allowed_cidrs &&
      data.viettelcloud_lb_listener.by_id.listener_timeout == viettelcloud_lb_listener.listener.listener_timeout &&
      data.viettelcloud_lb_listener.by_id.server_timeout == viettelcloud_lb_listener.listener.server_timeout &&
      data.viettelcloud_lb_listener.by_id.server_connecting_timeout == viettelcloud_lb_listener.listener.server_connecting_timeout &&
      data.viettelcloud_lb_listener.by_id.enable_h2 == viettelcloud_lb_listener.listener.enable_h2 &&
      data.viettelcloud_lb_listener.by_id.set_x_forwarded_for == viettelcloud_lb_listener.listener.set_x_forwarded_for &&
      data.viettelcloud_lb_listener.by_id.set_x_forwarded_port == viettelcloud_lb_listener.listener.set_x_forwarded_port &&
      data.viettelcloud_lb_listener.by_id.set_x_forwarded_proto == viettelcloud_lb_listener.listener.set_x_forwarded_proto &&
      data.viettelcloud_lb_listener.by_id.provisioning_status == viettelcloud_lb_listener.listener.provisioning_status &&
      data.viettelcloud_lb_listener.by_id.status == viettelcloud_lb_listener.listener.status &&
      data.viettelcloud_lb_listener.by_id.created_at == viettelcloud_lb_listener.listener.created_at &&
      length(data.viettelcloud_lb_listener.by_id.updated_at) > 0 &&
      data.viettelcloud_lb_listener.by_id.description == null &&
      data.viettelcloud_lb_listener.by_id.name == "tf-test-listener-lookup" &&
      data.viettelcloud_lb_listener.by_name.name == "  tf-test-listener-lookup  " &&
      data.viettelcloud_lb_listener.by_both == data.viettelcloud_lb_listener.by_id &&
      data.viettelcloud_lb_listener.by_name.id == data.viettelcloud_lb_listener.by_id.id &&
      data.viettelcloud_lb_listener.by_name.load_balancer_id == data.viettelcloud_lb_listener.by_id.load_balancer_id &&
      data.viettelcloud_lb_listener.by_name.protocol == data.viettelcloud_lb_listener.by_id.protocol &&
      data.viettelcloud_lb_listener.by_name.protocol_port == data.viettelcloud_lb_listener.by_id.protocol_port &&
      data.viettelcloud_lb_listener.by_name.server_group_id == data.viettelcloud_lb_listener.by_id.server_group_id &&
      data.viettelcloud_lb_listener.by_name.default_tls_cert_id == data.viettelcloud_lb_listener.by_id.default_tls_cert_id &&
      data.viettelcloud_lb_listener.by_name.sni_tls_cert_ids == data.viettelcloud_lb_listener.by_id.sni_tls_cert_ids &&
      data.viettelcloud_lb_listener.by_name.allowed_cidrs == data.viettelcloud_lb_listener.by_id.allowed_cidrs &&
      data.viettelcloud_lb_listener.by_name.listener_timeout == data.viettelcloud_lb_listener.by_id.listener_timeout &&
      data.viettelcloud_lb_listener.by_name.server_timeout == data.viettelcloud_lb_listener.by_id.server_timeout &&
      data.viettelcloud_lb_listener.by_name.server_connecting_timeout == data.viettelcloud_lb_listener.by_id.server_connecting_timeout &&
      data.viettelcloud_lb_listener.by_name.enable_h2 == data.viettelcloud_lb_listener.by_id.enable_h2 &&
      data.viettelcloud_lb_listener.by_name.set_x_forwarded_for == data.viettelcloud_lb_listener.by_id.set_x_forwarded_for &&
      data.viettelcloud_lb_listener.by_name.set_x_forwarded_port == data.viettelcloud_lb_listener.by_id.set_x_forwarded_port &&
      data.viettelcloud_lb_listener.by_name.set_x_forwarded_proto == data.viettelcloud_lb_listener.by_id.set_x_forwarded_proto &&
      data.viettelcloud_lb_listener.by_name.provisioning_status == data.viettelcloud_lb_listener.by_id.provisioning_status &&
      data.viettelcloud_lb_listener.by_name.status == data.viettelcloud_lb_listener.by_id.status &&
      data.viettelcloud_lb_listener.by_name.created_at == data.viettelcloud_lb_listener.by_id.created_at &&
      data.viettelcloud_lb_listener.by_name.updated_at == data.viettelcloud_lb_listener.by_id.updated_at &&
    data.viettelcloud_lb_listener.by_name.description == data.viettelcloud_lb_listener.by_id.description)
    error_message = "Listener readListener_idNameAndCombinedCriteria_completeStateReturned did not return the expected state."
  }
}
