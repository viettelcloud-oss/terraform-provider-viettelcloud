# Test index. Copy a run declaration below to find its block.
# Chain lookup (shared fixture state):
#   run "lookupMember_idNameAndCombinedCriteria_fullStateReturned" {

variable "lb_server_group_ids" {
  type        = list(string)
  description = "UUIDs of existing, ACTIVE server groups. The suite adds its subject member to the first one."
  validation {
    condition     = length(var.lb_server_group_ids) >= 1
    error_message = "Provide at least one server group UUID."
  }
}
variable "lb_datasource_member_address" {
  type        = string
  default     = "10.20.0.21"
  description = "Backend IP address of the subject member. It must differ from the addresses that the resource suite uses."
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

run "lookupMember_idNameAndCombinedCriteria_fullStateReturned" {
  command = apply
  module { source = "./fixtures/lb_member/datasource" }
  variables {
    server_group_id = var.lb_server_group_ids[0]
    address         = var.lb_datasource_member_address
  }
  assert {
    condition     = output.by_id == output.subject_lookup_view
    error_message = "The direct ID lookup did not return the full subject state."
  }
  assert {
    condition     = output.by_name == output.subject_lookup_view
    error_message = "The name lookup did not return the full subject state."
  }
  assert {
    condition = (
      output.by_all.id == upper(output.subject_lookup_view.id) &&
      output.by_all.name == " ${output.subject_lookup_view.name} " &&
      output.by_all.server_group_id == output.subject_lookup_view.server_group_id &&
      output.by_all.address == output.subject_lookup_view.address &&
      output.by_all.protocol_port == output.subject_lookup_view.protocol_port &&
      output.by_all.weight == 3 &&
      output.by_all.backup &&
      output.by_all.provisioning_status == "ACTIVE" &&
      output.by_all.created_at == output.subject_lookup_view.created_at
    )
    error_message = "The combined lookup did not preserve the configured criteria or map the subject state."
  }
}
