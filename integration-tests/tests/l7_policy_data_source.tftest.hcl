# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain datasource (shared fixture state; the fixture creates its own
# REDIRECT_PREFIX policy under listener_id and reads it back):
#   run "readPolicy_idNameAndListenerCriteria_completeStateReturned" {
#
# The data source looks up a policy by id, name, and listener_id. Missing
# criteria, whitespace, malformed UUIDs, zero and multiple matches, not-found,
# and DELETED policies are covered by unit tests.

variable "token" {
  description = "Viettel Cloud API access token used by this test."
  type        = string
  sensitive   = true
}

variable "project_id" {
  description = "Project UUID or slug used by this test."
  type        = string
}

variable "api_endpoint" {
  description = "Viettel Cloud API endpoint used by this test."
  type        = string
}

variable "listener_id" {
  description = "Existing HTTP or TERMINATED_HTTPS listener of an ACTIVE Load Balancer whose type is not NATIVE. The fixture adds its policy here."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Reads the policy by ID, by name, and by each supported combination with
# listener_id, and verifies that every one returns the managed policy with
# every field. The upper-case UUIDs and the untrimmed name keep the configured
# representation because they select the same policy.
run "readPolicy_idNameAndListenerCriteria_completeStateReturned" {
  command = apply

  module {
    source = "./fixtures/l7_policy/datasource"
  }

  variables {
    listener_id = var.listener_id
  }

  assert {
    condition = (
      data.viettelcloud_lb_l7_policy.by_id.id == viettelcloud_lb_l7_policy.subject.id &&
      lower(data.viettelcloud_lb_l7_policy.by_id.listener_id) == lower(var.listener_id) &&
      data.viettelcloud_lb_l7_policy.by_id.name == "tf-test-l7-policy-ds" &&
      data.viettelcloud_lb_l7_policy.by_id.description == "Data source subject" &&
      data.viettelcloud_lb_l7_policy.by_id.action == "REDIRECT_PREFIX" &&
      data.viettelcloud_lb_l7_policy.by_id.position == viettelcloud_lb_l7_policy.subject.position &&
      data.viettelcloud_lb_l7_policy.by_id.redirect_server_group_id == null &&
      data.viettelcloud_lb_l7_policy.by_id.redirect_url == null &&
      data.viettelcloud_lb_l7_policy.by_id.redirect_prefix == "https://example.com" &&
      data.viettelcloud_lb_l7_policy.by_id.redirect_http_code == 301 &&
      data.viettelcloud_lb_l7_policy.by_id.rule_count == 0 &&
      data.viettelcloud_lb_l7_policy.by_id.provisioning_status == "ACTIVE" &&
      length(data.viettelcloud_lb_l7_policy.by_id.status) > 0 &&
      data.viettelcloud_lb_l7_policy.by_id.created_at == viettelcloud_lb_l7_policy.subject.created_at &&
      length(data.viettelcloud_lb_l7_policy.by_id.updated_at) > 0
    )
    error_message = "The ID lookup did not return the managed L7 policy with every field."
  }

  assert {
    condition = alltrue([
      for lookup in [
        data.viettelcloud_lb_l7_policy.by_name,
        data.viettelcloud_lb_l7_policy.by_untrimmed_name,
        data.viettelcloud_lb_l7_policy.by_name_and_listener,
        data.viettelcloud_lb_l7_policy.by_id_and_listener,
        data.viettelcloud_lb_l7_policy.by_id_and_name,
        ] : (
        lower(lookup.id) == lower(viettelcloud_lb_l7_policy.subject.id) &&
        lower(lookup.listener_id) == lower(var.listener_id) &&
        lookup.action == "REDIRECT_PREFIX" &&
        lookup.redirect_prefix == "https://example.com" &&
        lookup.redirect_http_code == 301 &&
        lookup.created_at == viettelcloud_lb_l7_policy.subject.created_at
      )
    ])
    error_message = "A name or combined lookup did not return the managed L7 policy."
  }

  assert {
    condition = (
      data.viettelcloud_lb_l7_policy.by_upper_case_id.id == upper(viettelcloud_lb_l7_policy.subject.id) &&
      data.viettelcloud_lb_l7_policy.by_id_and_listener.listener_id == upper(var.listener_id) &&
      data.viettelcloud_lb_l7_policy.by_untrimmed_name.name == "  tf-test-l7-policy-ds  "
    )
    error_message = "The data source did not keep the configured upper-case UUIDs or the untrimmed name."
  }
}
