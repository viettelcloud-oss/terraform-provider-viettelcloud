# Test index. Copy a run declaration below to find its block.
# Chain lookup (shared fixture state):
#   run "queryServerGroup_createdSubject_allSelectorsAndFieldsReturned" {
# Run this file separately from the resource suite when they share a parent.
# Terraform teardown removes the subject and retains the existing parent.

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variable "server_group_load_balancer_id" {
  description = "Existing load balancer UUID for server group tests."
  type        = string
}

run "queryServerGroup_createdSubject_allSelectorsAndFieldsReturned" {
  command  = apply
  parallel = false
  module { source = "./fixtures/lb_server_group/datasource" }
  variables { load_balancer_id = var.server_group_load_balancer_id }
  assert {
    condition = (
      output.lookup_views.by_id == output.subject_lookup_view &&
      output.lookup_views.by_name == output.subject_lookup_view &&
      output.lookup_views.by_id_and_name == output.subject_lookup_view
    )
    error_message = "Server group selectors returned different objects or incomplete fields."
  }
  assert {
    condition = (
      output.by_normalized.id == upper(output.subject.id) &&
      output.by_normalized.name == " ${output.subject.name} " &&
      output.by_normalized.load_balancer_id == output.subject.load_balancer.id &&
      output.by_normalized.protocol == output.subject.protocol &&
      output.by_normalized.algorithm == output.subject.algorithm &&
      output.by_normalized.description == output.subject.description &&
      output.by_normalized.enable_h2 == output.subject.enable_h2 &&
      output.by_normalized.session_persistence == output.subject.session_persistence &&
      output.by_normalized.health_check == output.subject.health_check &&
      output.by_normalized.provisioning_status == output.subject.provisioning_status &&
      output.by_normalized.status == output.subject.status &&
      output.by_normalized.member_count == output.subject.member_count &&
      output.by_normalized.created_at == output.subject.created_at &&
      length(output.by_normalized.updated_at) > 0 &&
      length(output.by_id.updated_at) > 0 &&
      length(output.by_name.updated_at) > 0 &&
      length(output.by_id_and_name.updated_at) > 0
    )
    error_message = "The combined lookup lost fields or configured values."
  }
}
