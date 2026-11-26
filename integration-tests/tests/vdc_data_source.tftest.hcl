# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain datasource (shared fixture state; the fixture reads the existing vDC
# vdc_id and creates no infrastructure):
#   run "lookupVdc_existingVdcId_completeStateReturned" {
#   run "lookupVdc_nameAndZoneOrRegion_sameStateAsId" {
#   run "lookupVdc_untrimmedSelectors_configuredValuesPreserved" {
#
# Lookups by name use name with zone or name with region, the combinations
# that are unique in the test project. Missing criteria, blank criteria,
# malformed UUIDs, not-found, zero and multiple matches, an unknown zone, a
# null description, and names that contain each other are covered by unit
# tests.

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

variable "vdc_id" {
  description = "Existing vDC of the test project. Its name must be unique in its zone and in its region."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# AC1: the lookup by ID returns every attribute. description is not asserted,
# because it can be null.
run "lookupVdc_existingVdcId_completeStateReturned" {
  command = apply

  module {
    source = "./fixtures/vdc/datasource"
  }

  variables {
    vdc_id = var.vdc_id
  }

  assert {
    condition = (
      lower(data.viettelcloud_vdc.by_id.id) == lower(var.vdc_id) &&
      length(data.viettelcloud_vdc.by_id.name) > 0 &&
      length(data.viettelcloud_vdc.by_id.region) > 0 &&
      length(data.viettelcloud_vdc.by_id.zone) > 0 &&
      length(data.viettelcloud_vdc.by_id.region_id) > 0 &&
      length(data.viettelcloud_vdc.by_id.zone_id) > 0
    )
    error_message = "Lookup by ID returned incomplete vDC identity attributes."
  }

  assert {
    condition = (
      data.viettelcloud_vdc.by_id.ratio >= 0 &&
      data.viettelcloud_vdc.by_id.ratio <= 4
    )
    error_message = "Lookup by ID returned a ratio outside 0 to 4."
  }

  assert {
    condition     = [for stat in data.viettelcloud_vdc.by_id.stats : stat.name] == ["ram", "cpus", "gpus", "servers"]
    error_message = "Lookup by ID did not return the stats ram, cpus, gpus, servers in that order."
  }

  assert {
    condition = alltrue([
      for stat in data.viettelcloud_vdc.by_id.stats :
      length(stat.label) > 0 && stat.used != null && stat.total != null
    ])
    error_message = "Lookup by ID returned a stat without label, used, or total."
  }

  assert {
    condition     = [for stat in data.viettelcloud_vdc.by_id.stats : stat.total if stat.name == "servers"] == [-1]
    error_message = "Lookup by ID did not report servers.total as -1."
  }
}

# AC2 and AC3: name with zone and name with region select the vDC that the ID
# lookup returned. The filter path maps the list row straight into state, so
# every attribute must equal the ID lookup. stats[*].used is not compared,
# because usage can change between two reads.
run "lookupVdc_nameAndZoneOrRegion_sameStateAsId" {
  command = apply

  module {
    source = "./fixtures/vdc/datasource"
  }

  variables {
    vdc_id = var.vdc_id
  }

  assert {
    condition = alltrue([
      for lookup in [data.viettelcloud_vdc.by_name_and_zone, data.viettelcloud_vdc.by_name_and_region] :
      lookup.id == data.viettelcloud_vdc.by_id.id &&
      lookup.name == data.viettelcloud_vdc.by_id.name &&
      lookup.region == data.viettelcloud_vdc.by_id.region &&
      lookup.zone == data.viettelcloud_vdc.by_id.zone &&
      lookup.region_id == data.viettelcloud_vdc.by_id.region_id &&
      lookup.zone_id == data.viettelcloud_vdc.by_id.zone_id &&
      lookup.description == data.viettelcloud_vdc.by_id.description &&
      lookup.ratio == data.viettelcloud_vdc.by_id.ratio
    ])
    error_message = "Lookup by name with zone or region did not return the same vDC state as the ID lookup."
  }

  assert {
    condition = alltrue([
      for lookup in [data.viettelcloud_vdc.by_name_and_zone, data.viettelcloud_vdc.by_name_and_region] :
      [for stat in lookup.stats : [stat.name, stat.label, stat.total, stat.unit]] ==
      [for stat in data.viettelcloud_vdc.by_id.stats : [stat.name, stat.label, stat.total, stat.unit]]
    ])
    error_message = "Lookup by name with zone or region returned other stats than the ID lookup."
  }
}

# AC9: an upper-case ID and padded name, region, and zone select the same vDC
# and stay in state as configured.
run "lookupVdc_untrimmedSelectors_configuredValuesPreserved" {
  command = apply

  module {
    source = "./fixtures/vdc/datasource"
  }

  variables {
    vdc_id = var.vdc_id
  }

  assert {
    condition = (
      data.viettelcloud_vdc.untrimmed.id == upper(data.viettelcloud_vdc.by_id.id) &&
      data.viettelcloud_vdc.untrimmed.name == " ${data.viettelcloud_vdc.by_id.name} " &&
      data.viettelcloud_vdc.untrimmed.region == " ${data.viettelcloud_vdc.by_id.region} " &&
      data.viettelcloud_vdc.untrimmed.zone == " ${data.viettelcloud_vdc.by_id.zone} "
    )
    error_message = "Lookup with untrimmed selectors did not keep the configured values."
  }

  assert {
    condition = (
      data.viettelcloud_vdc.untrimmed.region_id == data.viettelcloud_vdc.by_id.region_id &&
      data.viettelcloud_vdc.untrimmed.zone_id == data.viettelcloud_vdc.by_id.zone_id &&
      data.viettelcloud_vdc.untrimmed.ratio == data.viettelcloud_vdc.by_id.ratio
    )
    error_message = "Lookup with untrimmed selectors selected another vDC."
  }
}
