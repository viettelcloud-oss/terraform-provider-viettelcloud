# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Chain lifecycle (state_key = "lifecycle"; configures region in the documented
# form):
#   run "createPlacementGroup_allAttributesConfigured_completeStateReturned" {
#   run "changeNameAndDescription_existingPlacementGroup_updatedInPlace" {
#   run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#   run "setUntrimmedName_existingPlacementGroup_configuredValuePreserved" {
#   run "planAgain_untrimmedName_stateStable" {
#   run "trimName_untrimmedNameInState_updatedInPlace" {
#   run "changePolicy_existingPlacementGroup_placementGroupReplaced" {
#
# Chain normalized_region (state_key = "normalized_region"; keeps an untrimmed
# region for the whole life of its own Placement Group):
#   run "createPlacementGroup_untrimmedRegion_configuredRegionPreserved" {
#   run "planAgain_untrimmedRegion_stateStable" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# Placement Group this chain creates for itself):
#   run "createPlacementGroup_importChainFixture_placementGroupCreated" {
#   run "planImport_existingPlacementGroupId_completeStateRestored" {

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

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain lifecycle.

# Creates a Placement Group and verifies the expected resource fields. This
# chain configures region exactly as the examples and documentation do, so the
# ordinary create path is the one exercised against the backend. Whitespace
# normalization for region lives in its own chain because region carries a
# RequiresReplace plan modifier and cannot change representation mid-chain.
run "createPlacementGroup_allAttributesConfigured_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "tf-test-pg-before"
    description = "Placement Group before update"
    policy      = "affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_placement_group.pg.id) > 0 &&
      viettelcloud_placement_group.pg.name == "tf-test-pg-before" &&
      viettelcloud_placement_group.pg.description == "Placement Group before update" &&
      viettelcloud_placement_group.pg.policy == "affinity" &&
      viettelcloud_placement_group.pg.region.name == "vn-central-1" &&
      length(viettelcloud_placement_group.pg.region_id) > 0 &&
      length(viettelcloud_placement_group.pg.project) > 0 &&
      viettelcloud_placement_group.pg.server_count != null &&
      viettelcloud_placement_group.pg.server_count == 0 &&
      length(viettelcloud_placement_group.pg.created_at) > 0 &&
      length(viettelcloud_placement_group.pg.updated_at) > 0
    )
    error_message = "Placement Group was not created with the expected values."
  }
}

# Renames and updates the description without replacing the Placement Group.
run "changeNameAndDescription_existingPlacementGroup_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "tf-test-pg-after"
    description = "Placement Group after update"
    policy      = "affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id == run.createPlacementGroup_allAttributesConfigured_completeStateReturned.placement_group_id &&
      viettelcloud_placement_group.pg.name == "tf-test-pg-after" &&
      viettelcloud_placement_group.pg.description == "Placement Group after update"
    )
    error_message = "Placement Group was replaced or was not updated."
  }
}

# Omitting an already configured description preserves its backend value.
run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name   = "tf-test-pg-after"
    policy = "affinity"
    region = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id == run.createPlacementGroup_allAttributesConfigured_completeStateReturned.placement_group_id &&
      viettelcloud_placement_group.pg.description == "Placement Group after update"
    )
    error_message = "Omitting description changed the Placement Group identity or description."
  }
}

# An explicit empty description clears it without replacing the Placement Group.
run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "tf-test-pg-after"
    description = ""
    policy      = "affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id == run.createPlacementGroup_allAttributesConfigured_completeStateReturned.placement_group_id &&
      viettelcloud_placement_group.pg.description == ""
    )
    error_message = "Clearing description replaced the Placement Group or did not persist an empty value."
  }
}

# The provider trims outer whitespace from name before the request and keeps the
# configured representation in state, so an untrimmed name is not drift.
run "setUntrimmedName_existingPlacementGroup_configuredValuePreserved" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "  tf-test-pg-after  "
    description = ""
    policy      = "affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id == run.createPlacementGroup_allAttributesConfigured_completeStateReturned.placement_group_id &&
      viettelcloud_placement_group.pg.name == "  tf-test-pg-after  " &&
      viettelcloud_placement_group.pg.region.name == "vn-central-1"
    )
    error_message = "The configured name representation was not preserved in place."
  }
}

# Replanning the untrimmed name must remain stable.
run "planAgain_untrimmedName_stateStable" {
  command   = plan
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "  tf-test-pg-after  "
    description = ""
    policy      = "affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id == run.createPlacementGroup_allAttributesConfigured_completeStateReturned.placement_group_id &&
      viettelcloud_placement_group.pg.name == "  tf-test-pg-after  " &&
      viettelcloud_placement_group.pg.region.name == "vn-central-1"
    )
    error_message = "The untrimmed Placement Group name caused drift or plan flapping."
  }
}

# Returning to the trimmed name changes the state representation without asking
# the backend for anything. The trimmed plan value equals the trimmed state
# value, so the provider sends no PATCH and refreshes with a GET instead. Only
# the request-level behavior is offline-only; the identity and the resulting
# state are observable here.
run "trimName_untrimmedNameInState_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "tf-test-pg-after"
    description = ""
    policy      = "affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id == run.createPlacementGroup_allAttributesConfigured_completeStateReturned.placement_group_id &&
      viettelcloud_placement_group.pg.name == "tf-test-pg-after" &&
      viettelcloud_placement_group.pg.description == "" &&
      viettelcloud_placement_group.pg.policy == "affinity"
    )
    error_message = "Trimming the configured name replaced the Placement Group or changed another value."
  }
}

# Changing the policy replaces the Placement Group with one that has the new
# policy. This run ends the chain and its Placement Group is destroyed at
# teardown.
run "changePolicy_existingPlacementGroup_placementGroupReplaced" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "tf-test-pg-after"
    description = ""
    policy      = "anti-affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id != run.createPlacementGroup_allAttributesConfigured_completeStateReturned.placement_group_id &&
      viettelcloud_placement_group.pg.policy == "anti-affinity" &&
      viettelcloud_placement_group.pg.name == "tf-test-pg-after" &&
      viettelcloud_placement_group.pg.region.name == "vn-central-1"
    )
    error_message = "Placement Group was not replaced after changing its policy."
  }
}


# Chain normalized_region.

# The provider trims outer whitespace from region before resolving it, so the
# backend never receives the padded string and reports the trimmed name; the
# configured representation is preserved in state instead. region carries a
# RequiresReplace plan modifier, so this chain keeps the same padded string for
# the whole life of its Placement Group. Changing the representation later would
# plan a replacement, which is why this scenario owns a chain rather than
# sitting inside the lifecycle chain.
run "createPlacementGroup_untrimmedRegion_configuredRegionPreserved" {
  command   = apply
  parallel  = true
  state_key = "normalized_region"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name   = "tf-test-pg-region"
    policy = "affinity"
    region = "  vn-central-1  "
  }

  assert {
    condition = (
      length(viettelcloud_placement_group.pg.id) > 0 &&
      viettelcloud_placement_group.pg.name == "tf-test-pg-region" &&
      viettelcloud_placement_group.pg.region.name == "  vn-central-1  " &&
      length(viettelcloud_placement_group.pg.region_id) > 0
    )
    error_message = "The configured region representation was not preserved on create."
  }
}

# Replanning the untrimmed region must remain stable and must not plan a
# replacement.
run "planAgain_untrimmedRegion_stateStable" {
  command   = plan
  parallel  = true
  state_key = "normalized_region"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name   = "tf-test-pg-region"
    policy = "affinity"
    region = "  vn-central-1  "
  }

  assert {
    condition = (
      viettelcloud_placement_group.pg.id == run.createPlacementGroup_untrimmedRegion_configuredRegionPreserved.placement_group_id &&
      viettelcloud_placement_group.pg.region.name == "  vn-central-1  " &&
      viettelcloud_placement_group.pg.region_id == run.createPlacementGroup_untrimmedRegion_configuredRegionPreserved.placement_group_region_id
    )
    error_message = "The untrimmed Placement Group region caused drift or planned a replacement."
  }
}


# Chain import.

# The import chain creates the Placement Group it later imports so no other
# chain has to hand it over. The description is deliberately non-empty: the
# import fixture leaves description unconfigured, so the value the plan reports
# can only come from the import refresh.
run "createPlacementGroup_importChainFixture_placementGroupCreated" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/placement_group/resource"
  }

  variables {
    name        = "tf-test-pg-import"
    description = "Placement Group for import"
    policy      = "anti-affinity"
    region      = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_placement_group.pg.id) > 0 &&
      viettelcloud_placement_group.pg.description == "Placement Group for import" &&
      viettelcloud_placement_group.pg.policy == "anti-affinity"
    )
    error_message = "The import chain's Placement Group was not created with the expected values."
  }
}

# Importing the Placement Group restores its state from the backend and plans
# no change. The run stays plan-only and uses its own state key. Otherwise the
# Placement Group that the previous run owns would gain a second owner, and
# teardown would delete it twice. The import block itself lives in the fixture
# module, because that module is this run's root module.
#
# Every input comes from the creating run's observed state, so nothing below is
# a literal that could drift away from the object under test. The assertions
# check the attributes that the import refresh alone supplies: description is
# unconfigured in the fixture, and the rest are computed. name, policy, and
# region are configured, so their planned values come from configuration and
# cannot prove a refresh. What keeps them honest is that any planned change at
# all makes every computed attribute unknown and fails this run. Their
# per-attribute mapping is proven offline by
# TestPopulatePlacementGroupResourceStateFillsNullStateAfterImport.
run "planImport_existingPlacementGroupId_completeStateRestored" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/placement_group/import"
  }

  variables {
    placement_group_id = run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_id
    name               = run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_name
    policy             = run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_policy
    region             = run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_region
  }

  assert {
    condition = (
      viettelcloud_placement_group.imported.id == run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_id &&
      viettelcloud_placement_group.imported.description == run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_description &&
      viettelcloud_placement_group.imported.region_id == run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_region_id &&
      viettelcloud_placement_group.imported.project == run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_project &&
      viettelcloud_placement_group.imported.server_count != null &&
      viettelcloud_placement_group.imported.server_count == run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_server_count &&
      viettelcloud_placement_group.imported.created_at == run.createPlacementGroup_importChainFixture_placementGroupCreated.placement_group_created_at &&
      length(viettelcloud_placement_group.imported.updated_at) > 0
    )
    error_message = "Importing the Placement Group did not restore the state the backend reports, or it planned a change."
  }
}
