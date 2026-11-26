# Load Balancer data-source lookup chain (shared fixture state).
# The fixture creates one Load Balancer in the existing VPC named by vpc_id and
# resolves it through every selector. The Load Balancer has a name that this
# file owns, so the name and name-and-region lookups select exactly one object.
# run "lookupLoadBalancer_subjectCreated_allSelectorsReturnSameState" {
# run "lookupByType_subjectInState_typeSelectorReturnsSameState" {
# run "planAgain_subjectAndLookups_stateStable" {
#
# Region-only lookup is not guaranteed unique in a shared project.
# The name-and-region and ID-and-region lookups cover the region criterion.
# Offline tests cover invalid criteria, empty results, ambiguity, and errors.
# Public IP configuration is outside the baseline contract, as in MR !20.
# Offline tests cover public IPv4 and IPv6 filters and IPv6 normalization.
# The backend can omit type, and the data source then reports null. In that
# case the by_type lookup does not exist and the combined lookup does not filter
# by type, so the type lookup run asserts which case applied.
# The by_type count needs a known type at plan time. The first run creates the
# subject and defers every lookup to apply, so the type lookup runs second,
# when the subject is in state and Terraform reads by_id during the plan.

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variable "vpc_id" {
  description = "ID of an existing VPC in the region where the Load Balancer is created."
  type        = string
}

variable "load_balancer_name" {
  description = "Unique name of the Load Balancer created for data-source lookups."
  type        = string
  default     = "tf-test-lb-ds"
}

variable "load_balancer_region" {
  description = "Region where the Load Balancer is created. The VPC must be in this region."
  type        = string
  default     = "vn-central-1"
}

run "lookupLoadBalancer_subjectCreated_allSelectorsReturnSameState" {
  command = apply

  module {
    source = "./fixtures/load_balancer/datasource"
  }

  variables {
    vpc_id   = var.vpc_id
    name     = var.load_balancer_name
    region   = var.load_balancer_region
    package  = "MEDIUM"
    topology = "SINGLE"
  }

  assert {
    condition = (
      data.viettelcloud_lb_loadbalancer.by_id.id == viettelcloud_lb_loadbalancer.subject.id &&
      data.viettelcloud_lb_loadbalancer.by_id.name == viettelcloud_lb_loadbalancer.subject.name &&
      data.viettelcloud_lb_loadbalancer.by_id.package == viettelcloud_lb_loadbalancer.subject.package &&
      data.viettelcloud_lb_loadbalancer.by_id.topology == viettelcloud_lb_loadbalancer.subject.topology &&
      data.viettelcloud_lb_loadbalancer.by_id.region == viettelcloud_lb_loadbalancer.subject.region.name &&
      data.viettelcloud_lb_loadbalancer.by_id.vpc_id == var.vpc_id &&
      data.viettelcloud_lb_loadbalancer.by_id.description == viettelcloud_lb_loadbalancer.subject.description &&
      data.viettelcloud_lb_loadbalancer.by_id.private_ip_id == viettelcloud_lb_loadbalancer.subject.private_ip_id &&
      data.viettelcloud_lb_loadbalancer.by_id.vip_address == viettelcloud_lb_loadbalancer.subject.vip_address &&
      length(data.viettelcloud_lb_loadbalancer.by_id.region_id) > 0 &&
      length(data.viettelcloud_lb_loadbalancer.by_id.vpc_name) > 0 &&
      length(data.viettelcloud_lb_loadbalancer.by_id.status) > 0 &&
      data.viettelcloud_lb_loadbalancer.by_id.provisioning_status == "ACTIVE" &&
      length(data.viettelcloud_lb_loadbalancer.by_id.created_at) > 0 &&
      length(data.viettelcloud_lb_loadbalancer.by_id.updated_at) > 0
    )
    error_message = "The ID lookup did not return the Load Balancer that the fixture created."
  }

  assert {
    condition = (
      data.viettelcloud_lb_loadbalancer.by_id.type == null ||
      data.viettelcloud_lb_loadbalancer.by_id.type == viettelcloud_lb_loadbalancer.subject.type
    ) && data.viettelcloud_lb_loadbalancer.by_id.access_log_enabled != null
    error_message = "The type must be null or equal to the resource type, and the access log status must be known."
  }

  assert {
    condition = alltrue([
      for result in [
        data.viettelcloud_lb_loadbalancer.by_name,
        data.viettelcloud_lb_loadbalancer.by_name_and_region,
        data.viettelcloud_lb_loadbalancer.by_id_and_name,
        data.viettelcloud_lb_loadbalancer.by_id_and_region,
        data.viettelcloud_lb_loadbalancer.by_vip_address,
        data.viettelcloud_lb_loadbalancer.by_package,
        data.viettelcloud_lb_loadbalancer.by_topology,
        data.viettelcloud_lb_loadbalancer.by_access_log,
      ] : result == data.viettelcloud_lb_loadbalancer.by_id
    ])
    error_message = "A filter lookup returned different state from the ID lookup."
  }

  assert {
    condition = alltrue([
      for key, value in data.viettelcloud_lb_loadbalancer.by_all_filters :
      contains(["id", "name", "region", "vip_address"], key) || value == data.viettelcloud_lb_loadbalancer.by_id[key]
    ])
    error_message = "The combined lookup returned different response fields from the ID lookup."
  }

  assert {
    condition = (
      data.viettelcloud_lb_loadbalancer.by_all_filters.id == upper(viettelcloud_lb_loadbalancer.subject.id) &&
      data.viettelcloud_lb_loadbalancer.by_all_filters.name == " ${viettelcloud_lb_loadbalancer.subject.name} " &&
      data.viettelcloud_lb_loadbalancer.by_all_filters.region == " ${viettelcloud_lb_loadbalancer.subject.region.name} " &&
      data.viettelcloud_lb_loadbalancer.by_all_filters.vip_address == " ${data.viettelcloud_lb_loadbalancer.by_id.vip_address} "
    )
    error_message = "The combined lookup did not preserve equivalent configured UUID casing and surrounding whitespace."
  }
}

run "lookupByType_subjectInState_typeSelectorReturnsSameState" {
  command = apply

  module {
    source = "./fixtures/load_balancer/datasource"
  }

  variables {
    vpc_id              = var.vpc_id
    name                = var.load_balancer_name
    region              = var.load_balancer_region
    package             = "MEDIUM"
    topology            = "SINGLE"
    type_lookup_enabled = true
  }

  # The type lookup exists only when the backend reports a type, so this
  # assertion makes a skipped type filter visible instead of a silent pass.
  assert {
    condition     = length(data.viettelcloud_lb_loadbalancer.by_type) == (data.viettelcloud_lb_loadbalancer.by_id.type == null ? 0 : 1)
    error_message = "The type lookup must exist exactly when the backend reports a type."
  }

  assert {
    condition = alltrue([
      for result in data.viettelcloud_lb_loadbalancer.by_type : result == data.viettelcloud_lb_loadbalancer.by_id
    ])
    error_message = "The type lookup returned different state from the ID lookup."
  }
}

run "planAgain_subjectAndLookups_stateStable" {
  command = plan

  module {
    source = "./fixtures/load_balancer/datasource"
  }

  variables {
    vpc_id              = var.vpc_id
    name                = var.load_balancer_name
    region              = var.load_balancer_region
    package             = "MEDIUM"
    topology            = "SINGLE"
    type_lookup_enabled = true
  }

  assert {
    condition = (
      data.viettelcloud_lb_loadbalancer.by_id.id == viettelcloud_lb_loadbalancer.subject.id &&
      data.viettelcloud_lb_loadbalancer.by_all_filters.id == upper(viettelcloud_lb_loadbalancer.subject.id) &&
      data.viettelcloud_lb_loadbalancer.by_all_filters.name == " ${viettelcloud_lb_loadbalancer.subject.name} " &&
      data.viettelcloud_lb_loadbalancer.by_all_filters.region == " ${viettelcloud_lb_loadbalancer.subject.region.name} " &&
      data.viettelcloud_lb_loadbalancer.by_all_filters.vip_address == " ${data.viettelcloud_lb_loadbalancer.by_id.vip_address} "
    )
    error_message = "A repeated plan did not preserve the lookup identity and configured representations."
  }
}
