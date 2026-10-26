# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain lifecycle (shared fixture state):
#   run "createElasticIp_ipv4OnlyConfig_ipv4AddressAllocated" {
#   run "refresh_existingElasticIp_statePreserved" {
#   run "updateDescription_existingElasticIp_descriptionUpdatedInPlace" {
#   run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "enableIpv6_ipv4OnlyElasticIp_elasticIpReplaced" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

run "createElasticIp_ipv4OnlyConfig_ipv4AddressAllocated" {
  command = apply

  module {
    source = "./fixtures/elastic_ip/resource"
  }

  variables {
    description = "Terraform integration test"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_elastic_ip.elastic_ip.id) > 0 &&
      viettelcloud_elastic_ip.elastic_ip.description == "Terraform integration test" &&
      viettelcloud_elastic_ip.elastic_ip.region.name == "vn-central-1" &&
      viettelcloud_elastic_ip.elastic_ip.enable_ipv4 &&
      !viettelcloud_elastic_ip.elastic_ip.enable_ipv6 &&
      length(viettelcloud_elastic_ip.elastic_ip.ip_address) > 0 &&
      viettelcloud_elastic_ip.elastic_ip.ipv6_address == null &&
      length(viettelcloud_elastic_ip.elastic_ip.status) > 0 &&
      length(viettelcloud_elastic_ip.elastic_ip.created_at) > 0 &&
      length(viettelcloud_elastic_ip.elastic_ip.updated_at) > 0
    )
    error_message = "Elastic IP resource did not expose the expected values."
  }
}

run "refresh_existingElasticIp_statePreserved" {
  command = plan

  module {
    source = "./fixtures/elastic_ip/resource"
  }

  variables {
    description = "Terraform integration test"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_elastic_ip.elastic_ip.id == run.createElasticIp_ipv4OnlyConfig_ipv4AddressAllocated.elastic_ip_id &&
      viettelcloud_elastic_ip.elastic_ip.description == "Terraform integration test" &&
      viettelcloud_elastic_ip.elastic_ip.region.name == "vn-central-1" &&
      viettelcloud_elastic_ip.elastic_ip.enable_ipv4 &&
      !viettelcloud_elastic_ip.elastic_ip.enable_ipv6 &&
      length(viettelcloud_elastic_ip.elastic_ip.ip_address) > 0 &&
      viettelcloud_elastic_ip.elastic_ip.ipv6_address == null &&
      length(viettelcloud_elastic_ip.elastic_ip.status) > 0 &&
      length(viettelcloud_elastic_ip.elastic_ip.created_at) > 0 &&
      length(viettelcloud_elastic_ip.elastic_ip.updated_at) > 0
    )
    error_message = "Elastic IP state was not preserved during refresh."
  }
}

run "updateDescription_existingElasticIp_descriptionUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/elastic_ip/resource"
  }

  variables {
    description = "Terraform integration test updated"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_elastic_ip.elastic_ip.id == run.createElasticIp_ipv4OnlyConfig_ipv4AddressAllocated.elastic_ip_id &&
      viettelcloud_elastic_ip.elastic_ip.description == "Terraform integration test updated" &&
      viettelcloud_elastic_ip.elastic_ip.enable_ipv4 &&
      !viettelcloud_elastic_ip.elastic_ip.enable_ipv6
    )
    error_message = "Updating description did not update the Elastic IP in place."
  }
}

run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/elastic_ip/resource"
  }

  variables {
    region = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_elastic_ip.elastic_ip.id == run.createElasticIp_ipv4OnlyConfig_ipv4AddressAllocated.elastic_ip_id &&
      viettelcloud_elastic_ip.elastic_ip.description == "Terraform integration test updated"
    )
    error_message = "Omitting description unexpectedly changed the Elastic IP description or replaced the Elastic IP."
  }
}

run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command = apply

  module {
    source = "./fixtures/elastic_ip/resource"
  }

  variables {
    description = ""
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_elastic_ip.elastic_ip.id == run.createElasticIp_ipv4OnlyConfig_ipv4AddressAllocated.elastic_ip_id &&
      viettelcloud_elastic_ip.elastic_ip.description == ""
    )
    error_message = "Clearing description did not empty the Elastic IP description."
  }
}

run "enableIpv6_ipv4OnlyElasticIp_elasticIpReplaced" {
  command = apply

  module {
    source = "./fixtures/elastic_ip/resource"
  }

  variables {
    description = "Terraform integration test"
    enable_ipv6 = true
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_elastic_ip.elastic_ip.id != run.createElasticIp_ipv4OnlyConfig_ipv4AddressAllocated.elastic_ip_id &&
      viettelcloud_elastic_ip.elastic_ip.description == "Terraform integration test" &&
      viettelcloud_elastic_ip.elastic_ip.enable_ipv4 &&
      viettelcloud_elastic_ip.elastic_ip.enable_ipv6 &&
      viettelcloud_elastic_ip.elastic_ip.ip_address != null &&
      viettelcloud_elastic_ip.elastic_ip.ip_address != "" &&
      viettelcloud_elastic_ip.elastic_ip.ipv6_address != null &&
      viettelcloud_elastic_ip.elastic_ip.ipv6_address != ""
    )
    error_message = "Elastic IP address-family replacement did not expose the expected values."
  }
}
