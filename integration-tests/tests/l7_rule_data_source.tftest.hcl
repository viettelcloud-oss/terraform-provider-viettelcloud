# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain datasource (shared fixture state; the fixture creates its own HEADER
# rule under l7_policy_id and reads it back):
#   run "readRule_parentAndRuleId_completeStateReturned" {
#
# The data source looks up a rule by ID only. Lookup by type, value, or key is
# N/A because the baseline provides no such criterion. Missing criteria,
# malformed UUIDs, and not-found diagnostics are covered by unit tests.

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

variable "l7_policy_id" {
  description = "Existing L7 policy on an HTTP listener of an ACTIVE Load Balancer. The fixture adds its rule here."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Reads the rule by its policy and rule IDs, and verifies that every field
# matches the managed rule. The upper-case lookup keeps the configured IDs
# because they name the same objects.
run "readRule_parentAndRuleId_completeStateReturned" {
  command = apply

  module {
    source = "./fixtures/l7_rule/datasource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
  }

  assert {
    condition = (
      data.viettelcloud_lb_l7_rule.by_id.id == viettelcloud_lb_l7_rule.subject.id &&
      data.viettelcloud_lb_l7_rule.by_id.l7_policy_id == var.l7_policy_id &&
      data.viettelcloud_lb_l7_rule.by_id.type == "HEADER" &&
      data.viettelcloud_lb_l7_rule.by_id.compare_type == "EQUAL_TO" &&
      data.viettelcloud_lb_l7_rule.by_id.key == "X-Tf-Test-Ds" &&
      data.viettelcloud_lb_l7_rule.by_id.value == "tf-test-ds" &&
      data.viettelcloud_lb_l7_rule.by_id.invert == true &&
      data.viettelcloud_lb_l7_rule.by_id.provisioning_status == viettelcloud_lb_l7_rule.subject.provisioning_status &&
      length(data.viettelcloud_lb_l7_rule.by_id.status) > 0 &&
      data.viettelcloud_lb_l7_rule.by_id.created_at == viettelcloud_lb_l7_rule.subject.created_at &&
      length(data.viettelcloud_lb_l7_rule.by_id.updated_at) > 0
    )
    error_message = "The data source did not return the managed L7 rule with every field."
  }

  assert {
    condition = (
      data.viettelcloud_lb_l7_rule.by_upper_case_ids.id == upper(viettelcloud_lb_l7_rule.subject.id) &&
      data.viettelcloud_lb_l7_rule.by_upper_case_ids.l7_policy_id == upper(var.l7_policy_id) &&
      data.viettelcloud_lb_l7_rule.by_upper_case_ids.value == "tf-test-ds"
    )
    error_message = "The data source did not keep the configured upper-case IDs."
  }
}
