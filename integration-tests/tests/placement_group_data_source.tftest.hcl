# Placement Group data-source lookup chain (shared fixture state)
# run "lookupPlacementGroup_subjectCreated_allSelectorsReturnSameState" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variable "placement_group_name" {
  description = "Unique name of the Placement Group created for data-source lookups."
  type        = string
  default     = "tf-test-placement-group-ds"
}

variable "placement_group_policy" {
  description = "Policy of the Placement Group created for data-source lookups."
  type        = string
  default     = "affinity"
}

variable "placement_group_region" {
  description = "Region where the Placement Group is created."
  type        = string
  default     = "vn-central-1"
}

run "lookupPlacementGroup_subjectCreated_allSelectorsReturnSameState" {
  command = apply

  module {
    source = "./fixtures/placement_group/datasource"
  }

  variables {
    placement_group_name   = var.placement_group_name
    placement_group_policy = var.placement_group_policy
    placement_group_region = var.placement_group_region
  }

  assert {
    condition = (
      data.viettelcloud_placement_group.by_id.id == viettelcloud_placement_group.subject.id &&
      data.viettelcloud_placement_group.by_id.name == viettelcloud_placement_group.subject.name &&
      data.viettelcloud_placement_group.by_id.policy == viettelcloud_placement_group.subject.policy &&
      data.viettelcloud_placement_group.by_id.region == viettelcloud_placement_group.subject.region.name &&
      data.viettelcloud_placement_group.by_id.description == viettelcloud_placement_group.subject.description &&
      length(data.viettelcloud_placement_group.by_id.region_id) > 0 &&
      length(data.viettelcloud_placement_group.by_id.project) > 0 &&
      length(data.viettelcloud_placement_group.by_id.created_at) > 0 &&
      length(data.viettelcloud_placement_group.by_id.updated_at) > 0
    )
    error_message = "Lookup by ID returned incomplete placement group state."
  }

  assert {
    condition = (
      data.viettelcloud_placement_group.by_id.server_count == null ||
      data.viettelcloud_placement_group.by_id.server_count >= 0
    )
    error_message = "Lookup by ID reported a negative server count."
  }

  # The filter path maps the list row straight into state instead of re-reading
  # the object, so a name lookup has to return everything the direct read
  # returns, not merely the same ID.
  assert {
    condition = (
      lower(data.viettelcloud_placement_group.by_name.id) == lower(data.viettelcloud_placement_group.by_id.id) &&
      data.viettelcloud_placement_group.by_name.name == data.viettelcloud_placement_group.by_id.name &&
      data.viettelcloud_placement_group.by_name.description == data.viettelcloud_placement_group.by_id.description &&
      data.viettelcloud_placement_group.by_name.policy == data.viettelcloud_placement_group.by_id.policy &&
      data.viettelcloud_placement_group.by_name.region == data.viettelcloud_placement_group.by_id.region &&
      data.viettelcloud_placement_group.by_name.region_id == data.viettelcloud_placement_group.by_id.region_id &&
      data.viettelcloud_placement_group.by_name.project == data.viettelcloud_placement_group.by_id.project &&
      data.viettelcloud_placement_group.by_name.server_count == data.viettelcloud_placement_group.by_id.server_count &&
      data.viettelcloud_placement_group.by_name.created_at == data.viettelcloud_placement_group.by_id.created_at &&
      data.viettelcloud_placement_group.by_name.updated_at == data.viettelcloud_placement_group.by_id.updated_at
    )
    error_message = "Lookup by unique exact name did not return the same placement group state as the ID lookup."
  }

  assert {
    condition     = data.viettelcloud_placement_group.by_name_and_policy.id == data.viettelcloud_placement_group.by_id.id
    error_message = "Lookup by name and policy selected the wrong placement group."
  }

  assert {
    condition     = data.viettelcloud_placement_group.by_name_and_region.id == data.viettelcloud_placement_group.by_id.id
    error_message = "Lookup by name and region selected the wrong placement group."
  }

  assert {
    condition     = data.viettelcloud_placement_group.by_id_and_name.id == data.viettelcloud_placement_group.by_id.id
    error_message = "Lookup by ID and name selected the wrong placement group."
  }

  # Every criterion the ID lookup already proved, spelled the way a practitioner
  # might: an upper-cased ID and padded strings must select the same placement
  # group and stay in state as configured.
  assert {
    condition = (
      data.viettelcloud_placement_group.by_all_filters.id == upper(viettelcloud_placement_group.subject.id) &&
      data.viettelcloud_placement_group.by_all_filters.name == " ${viettelcloud_placement_group.subject.name} " &&
      data.viettelcloud_placement_group.by_all_filters.policy == " ${viettelcloud_placement_group.subject.policy} " &&
      data.viettelcloud_placement_group.by_all_filters.region == " ${viettelcloud_placement_group.subject.region.name} " &&
      data.viettelcloud_placement_group.by_all_filters.region_id == data.viettelcloud_placement_group.by_id.region_id
    )
    error_message = "Combined lookup did not select the target or preserve equivalent configured representations."
  }
}
