# Test index. Copy a run declaration to find its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain network (shared setup; state_key = "network"):
#   run "createVpc_isolatedNetwork_validVpcIdReturned" {
#
# Chain base (state_key = "base"):
#   run "createServer_optionalServerValuesOmitted_runtimeVolumesAndPrivateIpReturned" {
#   run "planAgain_createdServer_stateStable" {
#
# Chain mutable_fields (state_key = "mutable_fields"):
#   run "createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated" {
#   run "changeNameDescriptionAndBandwidth_existingServer_updatedInPlace" {
#   run "omitDescription_serverDescriptionSet_descriptionPreservedInPlace" {
#   run "clearDescriptionAndSetUntrimmedName_serverDescriptionSet_emptyDescriptionAndNamePreserved" {
#   run "reapply_serverWithClearedDescriptionAndUntrimmedName_identityAndStatePreserved" {
#
# Chain bandwidth (state_key = "bandwidth"):
#   run "createServer_bandwidth400_serverCreated" {
#   run "changeBandwidth_serverWithBandwidth400_updatedInPlace" {
#
# Chain private_ip (state_key = "private_ip"):
#   run "createServer_singleSubnetAttachment_privateIpAllocated" {
#   run "attachSecondSubnet_oneSubnetAttachment_twoPrivateIpsWithFirstPreserved" {
#   run "reverseOrder_twoSubnetAttachments_privateIpIdsReordered" {
#   run "removeFirstSubnet_twoSubnetAttachments_secondPrivateIpPreserved" {
#   run "returnToFirstSubnet_secondSubnetAttachment_privateIpReallocated" {
#   run "deleteSubnet_detachedSecondSubnet_subnetRemovedAndServerPreserved" {
#
# Chain existing_ip (state_key = "existing_ip"):
#   run "createServer_singleSubnetIpBeforeManagedIpAttach_serverCreated" {
#   run "attachManagedPrivateIp_serverWithSubnetIp_twoAttachmentsWithManagedIpPreserved" {
#   run "detachManagedPrivateIp_managedIpAttachment_managedIpPreserved" {
#
# Chain elastic_ip (state_key = "elastic_ip"):
#   run "createServer_noElasticIpConfig_serverRunningWithoutElasticIps" {
#   run "attachElasticIp_serverWithoutElasticIps_allocatedAddressReturned" {
#   run "attachSecondElasticIp_oneElasticIpAttachment_firstAttachmentPreserved" {
#   run "planAgain_referencesAndAttachmentsOmitted_stateStable" {
#   run "removeSecondElasticIp_twoElasticIpAttachments_firstAttachmentPreserved" {
#   run "setEmptyElasticIpList_twoElasticIpAttachments_elasticIpsDetachedAndPrivateIpPreserved" {
#   run "attachElasticIpBeforeTeardown_noElasticIpAttachments_ownedElasticIpDeletedWithServer" {
#
# Chain power (state_key = "power"):
#   run "createServer_powerStateOmitted_serverCreated" {
#   run "requestShutdown_runningServer_serverStoppedInPlace" {
#   run "planAgain_stoppedServer_powerStateStable" {
#   run "requestRunning_stoppedServer_serverStartedInPlace" {
#
# Chain rebuild (state_key = "rebuild"):
#   run "createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned" {
#   run "changeImage_existingBootImage_serverRebuiltInPlace" {
#   run "restoreOriginalImage_rebuiltServer_serverRebuiltInPlace" {
#   run "rebuildAndResizeRunningServer_newFlavorAndImage_rebuiltAndResizedInPlace" {
#
# Chain user_data (state_key = "user_data"):
#   run "createServer_userDataOmitted_serverCreated" {
#   run "configureUserData_serverWithoutUserData_serverAndPrivateIpReplaced" {
#   run "planAgain_configuredUserData_serverIdentityPreserved" {
#
# Chain data_volume (state_key = "data_volume"):
#   run "createServer_dataVolumeSize20_serverCreated" {
#   run "increaseBootVolumeSize_existingServer_onlyBootVolumeResizedInPlace" {
#   run "increaseDataVolumeSize_existingServer_onlySecondDataVolumeResizedInPlace" {
#   run "increaseBootAndDataVolumeSizes_existingServer_allVolumesResizedInPlace" {
#   run "clearDeleteOnTermination_volumesDeletedOnTermination_volumesKeptInPlace" {
#   run "omitDeleteOnTermination_volumesKeptOnTermination_keptValuePreserved" {
#   run "restoreDeleteOnTermination_volumesKeptOnTermination_volumesDeletedInPlace" {
#   run "removeAndResizeVolumes_twoOwnedVolumes_retainedVolumeIdPreserved" {
#   run "planAgain_removedAndResizedVolumes_noDrift" {
#
# Chain to_custom (state_key = "to_custom"):
#   run "createServer_predefinedFlavorForCustomSwitch_serverCreated" {
#   run "switchToCustom_predefinedFlavor_serverResizedInPlace" {
#
# Chain custom_resize (state_key = "custom_resize"):
#   run "createServer_predefinedFlavorForCapacityResize_serverCreated" {
#   run "switchToCustom_predefinedFlavorForCapacityResize_serverResizedInPlace" {
#   run "changeCapacity_customFlavor_serverResizedInPlace" {
#
# Chain to_predefined (state_key = "to_predefined"):
#   run "createServer_predefinedFlavorForRoundTripResize_serverCreated" {
#   run "switchToCustom_predefinedFlavorForRoundTripResize_serverResizedInPlace" {
#   run "changeCapacity_customFlavorBeforePredefinedSwitch_serverResizedInPlace" {
#   run "switchToPredefined_resizedCustomFlavor_serverResizedInPlace" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import):
#   run "createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned" {
#   run "planImport_existingServerId_runtimeAndAttachmentStateRestored" {
#
# Chain offline_operations (state_key = "offline_operations"):
#   run "createServer_stoppedServer_serverCreatedShutdown" {
#   run "resizeStoppedServer_customFlavor_serverResizedInPlaceWhileShutdown" {
#   run "rebuildStoppedServer_newImage_serverRebuiltInPlaceWhileShutdown" {
#   run "rebuildAndStartServer_stoppedAfterOfflineOperations_serverRebuiltAndStarted" {

# The Server suite shares one VPC across 17 independent server chains.
# Import plans leave each server with the chain that creates it.

# Chain selectors (state_key "selectors"): shared network setup, then cleanup.
#   run "createServer_catalogCriteria_selectorsPreserved" {
#   run "planAgain_catalogCriteria_stateStable" {
#   run "changeDataVolumeCriteria_sameCatalogEntry_serverIdentityPreserved" {
#   run "changeBootVolumeTypeSelector_existingSelector_serverReplaced" {
#   run "changeImageSelector_existingSelector_serverRebuiltInPlace" {
# A data volume type change has no run. The backend rejects a retype of an
# attached volume, so offline tests cover the in-place retype.


# Chain data_volume_attachments (shared VPC; state_key = "data_volume_attachments"):
# Plan-only import uses state_key = "data_volume_attachments_import".
#   run "createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned" {
#   run "planServer_mixedAttachments_noDrift" {
#   run "detachSecondExistingVolume_multipleCreateAttachments_otherVolumesPreserved" {
#   run "reverseVolumes_mixedAttachments_volumeIdsPreserved" {
#   run "replaceExistingVolume_mixedAttachments_serverAndNewVolumePreserved" {
#   run "resizeNewVolume_mixedAttachments_volumeExtendedInPlace" {
#   run "planServer_resizedVolume_backendIopsPreserved" {
#   run "removeNewVolume_defaultDeletion_volumeRemovedAndExistingPreserved" {
#   run "addNewVolume_existingAttachment_volumeCreatedWithoutServerReplacement" {
#   run "planServer_addedNewVolume_noDrift" {
#   run "keepNewVolume_mixedAttachments_deletionFlagUpdatedInPlace" {
#   run "omitVolumes_mixedAttachments_listAndDeletionFlagPreserved" {
#   run "planAgain_volumesOmittedAfterRename_noDrift" {
#   run "importServer_mixedAttachments_existingOwnershipRestored" {
#   run "clearVolumes_mixedAttachments_existingAndKeptNewVolumesAvailable" {
# Detached-volume cleanup uses state_key = "data_volume_attachments_cleanup":
#   run "adoptKeptVolume_detachedNewVolume_singleCleanupOwnerEstablished" {
# Teardown deletes the adopted volume before the attachment chain.
# The Server releases ownership before adoption, so each object has one owner.
# The API does not identify attachment creators. Import therefore records
# every data attachment as existing with delete_on_termination false.
# Multiattach needs a compatible volume type. Offline tests cover this case.
# Offline tests also cover failures, timeout, cancellation, and external drift.
# Busy operation locks and failed refresh after volume allocation use offline
# tests because the real API does not offer deterministic fault injection.


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

variable "keypair_id" {
  type    = string
  default = null
}

variable "placementgroup_id" {
  type    = string
  default = null
}

variable "security_group_id" {
  type    = string
  default = null
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# The SDK catalog contains encrypted and unencrypted types named hdd_st1.
# The encryption criterion selects the unencrypted HDD 500 entry in this zone.
variables {
  region      = "vn-central-1"
  zone        = "vn-central-1a"
  image       = "cirros"
  volume_type = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
  bandwidth   = 300

  flavor_kind            = "predefined"
  predefined_flavor_name = "b1.micro2x"

  key_pair_id        = var.keypair_id
  placement_group_id = var.placementgroup_id
  security_group_ids = var.security_group_id == null ? null : [var.security_group_id]
}


run "createVpc_isolatedNetwork_validVpcIdReturned" {
  command   = apply
  state_key = "network"

  module {
    source = "./fixtures/server/network"
  }

  variables {
    name   = "tf-test-server-vpc"
    cidr   = "10.36.0.0/16"
    region = var.region
  }

  assert {
    condition     = length(viettelcloud_vpc.shared.id) > 0 && can(regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", viettelcloud_vpc.shared.id))
    error_message = "The VPC creation did not return a valid UUID."
  }
}

# Chain base.

run "createServer_optionalServerValuesOmitted_runtimeVolumesAndPrivateIpReturned" {
  command   = apply
  parallel  = true
  state_key = "base"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-base"
    subnet_index       = 101
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "tf-test-server-base-before"
    server_description = null
  }

  assert {
    condition = (
      length(viettelcloud_server.server.id) > 0 &&
      viettelcloud_server.server.name == "tf-test-server-base-before" &&
      viettelcloud_server.server.description == "" &&
      viettelcloud_server.server.zone.name == "vn-central-1a" &&
      viettelcloud_server.server.quantity == 1 &&
      viettelcloud_server.server.bandwidth == 300 &&
      viettelcloud_server.server.power_state == "running" &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x" &&
      viettelcloud_server.server.flavor.vcpus == null &&
      viettelcloud_server.server.flavor.ram == null &&
      try(viettelcloud_server.server.key_pair.id, null) == var.key_pair_id &&
      try(viettelcloud_server.server.placement_group.id, null) == var.placement_group_id &&
      (var.security_group_ids == null ? true : length(setsubtract(var.security_group_ids, viettelcloud_server.server.security_groups[*].id)) == 0) &&
      length(viettelcloud_server.server.status) > 0 &&
      length(viettelcloud_server.server.server_type) > 0 &&
      length(viettelcloud_server.server.created_at) > 0 &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      length(viettelcloud_server.server.private_ips[0].id) > 0 &&
      length(viettelcloud_server.server.private_ips[0].ip_address) > 0 &&
      viettelcloud_server.server.private_ips[0].delete_on_termination &&
      length(viettelcloud_server.server.elastic_ips) == 0 &&
      length(viettelcloud_server.server.data_volume_ids) == 1 &&
      viettelcloud_server.server.boot.volume_size == 30 &&
      viettelcloud_server.server.boot.delete_on_termination &&
      viettelcloud_server.server.data_volumes[0].delete_on_termination
    )
    error_message = "Server was not created with the expected runtime, volume, and private IP state."
  }
}

run "planAgain_createdServer_stateStable" {
  command   = plan
  parallel  = true
  state_key = "base"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-base"
    subnet_index       = 101
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "tf-test-server-base-before"
    server_description = null
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_optionalServerValuesOmitted_runtimeVolumesAndPrivateIpReturned.server_id &&
      try(viettelcloud_server.server.key_pair.id, null) == var.key_pair_id &&
      try(viettelcloud_server.server.placement_group.id, null) == var.placement_group_id &&
      (var.security_group_ids == null ? true : length(setsubtract(var.security_group_ids, viettelcloud_server.server.security_groups[*].id)) == 0) &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_optionalServerValuesOmitted_runtimeVolumesAndPrivateIpReturned.private_ip_id &&
      viettelcloud_server.server.data_volume_ids == run.createServer_optionalServerValuesOmitted_runtimeVolumesAndPrivateIpReturned.data_volume_ids &&
      viettelcloud_server.server.boot.image.name == "cirros" &&
      viettelcloud_server.server.boot.volume_type == merge(var.volume_type, { id = viettelcloud_server.server.boot.volume_type.id }) &&
      viettelcloud_server.server.boot.volume_size == 30 &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x"
    )
    error_message = "Replanning the created configuration reported drift or planned a replacement."
  }
}


# Chain mutable_fields.

run "createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-mutable-fields"
    subnet_index       = 111
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "tf-test-server-mutable-fields-before"
    server_description = null
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The mutable-fields chain's prerequisite server was not created."
  }
}

run "changeNameDescriptionAndBandwidth_existingServer_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-mutable-fields"
    subnet_index       = 111
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "tf-test-server-mutable-fields-after"
    server_description = "updated server"
    bandwidth          = 400
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated.server_id &&
      viettelcloud_server.server.name == "tf-test-server-mutable-fields-after" &&
      viettelcloud_server.server.description == "updated server" &&
      viettelcloud_server.server.bandwidth == 400 &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x" &&
      viettelcloud_server.server.flavor.vcpus == null &&
      viettelcloud_server.server.flavor.ram == null &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated.private_ip_id &&
      length(viettelcloud_server.server.data_volume_ids) == 1
    )
    error_message = "Server name, description, or bandwidth was not updated in place."
  }
}

run "omitDescription_serverDescriptionSet_descriptionPreservedInPlace" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-mutable-fields"
    subnet_index       = 111
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "tf-test-server-mutable-fields-after"
    server_description = null
    bandwidth          = 400
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated.server_id &&
      viettelcloud_server.server.description == "updated server"
    )
    error_message = "Omitting description changed the backend value or replaced the server."
  }
}

run "clearDescriptionAndSetUntrimmedName_serverDescriptionSet_emptyDescriptionAndNamePreserved" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-mutable-fields"
    subnet_index       = 111
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "  tf-test-server-mutable-fields-after  "
    server_description = ""
    bandwidth          = 400
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated.server_id &&
      viettelcloud_server.server.name == "  tf-test-server-mutable-fields-after  " &&
      viettelcloud_server.server.description == ""
    )
    error_message = "Clearing description or preserving the configured name representation failed."
  }
}

run "reapply_serverWithClearedDescriptionAndUntrimmedName_identityAndStatePreserved" {
  command   = apply
  parallel  = true
  state_key = "mutable_fields"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-mutable-fields"
    subnet_index       = 111
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "  tf-test-server-mutable-fields-after  "
    server_description = ""
    bandwidth          = 400
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated.server_id &&
      try(viettelcloud_server.server.key_pair.id, null) == var.key_pair_id &&
      try(viettelcloud_server.server.placement_group.id, null) == var.placement_group_id &&
      (var.security_group_ids == null ? true : length(setsubtract(var.security_group_ids, viettelcloud_server.server.security_groups[*].id)) == 0) &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_nameSetDescriptionOmittedAndBandwidth300_serverCreated.private_ip_id &&
      viettelcloud_server.server.name == "  tf-test-server-mutable-fields-after  " &&
      viettelcloud_server.server.description == "" &&
      viettelcloud_server.server.bandwidth == 400 &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x" &&
      viettelcloud_server.server.flavor.vcpus == null &&
      viettelcloud_server.server.flavor.ram == null &&
      viettelcloud_server.server.boot.image.name == "cirros" &&
      viettelcloud_server.server.boot.volume_type == merge(var.volume_type, { id = viettelcloud_server.server.boot.volume_type.id }) &&
      viettelcloud_server.server.boot.volume_size == 30
    )
    error_message = "Reapplying equivalent configuration changed server identity or managed state."
  }
}


# Chain bandwidth.

run "createServer_bandwidth400_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "bandwidth"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    bandwidth          = 400
    name_prefix        = "tf-test-server-bandwidth"
    subnet_index       = 121
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "  tf-test-server-bandwidth-after  "
    server_description = ""
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The bandwidth chain's prerequisite server was not created."
  }
}

run "changeBandwidth_serverWithBandwidth400_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "bandwidth"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix        = "tf-test-server-bandwidth"
    subnet_index       = 121
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name        = "  tf-test-server-bandwidth-after  "
    server_description = ""
    bandwidth          = 500
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_bandwidth400_serverCreated.server_id &&
      viettelcloud_server.server.bandwidth == 500 &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x" &&
      viettelcloud_server.server.flavor.vcpus == null &&
      viettelcloud_server.server.flavor.ram == null &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_bandwidth400_serverCreated.private_ip_id
    )
    error_message = "Changing only bandwidth did not update in place."
  }
}


# Chain private_ip.

run "createServer_singleSubnetAttachment_privateIpAllocated" {
  command   = apply
  parallel  = true
  state_key = "private_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-private-ip"
    subnet_index = 131
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition = (
      length(viettelcloud_server.server.id) > 0 &&
      viettelcloud_server.server.name == "tf-test-server-private-ip-server" &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      length(viettelcloud_server.server.private_ips[0].id) > 0 &&
      length(viettelcloud_server.server.private_ips[0].ip_address) > 0 &&
      viettelcloud_server.server.private_ips[0].kind == "subnet" &&
      viettelcloud_server.server.private_ips[0].delete_on_termination &&
      length(viettelcloud_server.server.data_volume_ids) == 1
    )
    error_message = "Server was not created with a single allocated subnet private IP."
  }
}

run "attachSecondSubnet_oneSubnetAttachment_twoPrivateIpsWithFirstPreserved" {
  command   = apply
  parallel  = true
  state_key = "private_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix     = "tf-test-server-private-ip"
    subnet_index    = 131
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    private_ip_mode = "both"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_singleSubnetAttachment_privateIpAllocated.server_id &&
      length(viettelcloud_server.server.private_ips) == 2 &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_singleSubnetAttachment_privateIpAllocated.private_ip_id &&
      length(viettelcloud_server.server.private_ips[1].id) > 0 &&
      length(viettelcloud_server.server.private_ips[1].ip_address) > 0
    )
    error_message = "Adding a second subnet attachment did not extend the private IP list in place."
  }
}

run "reverseOrder_twoSubnetAttachments_privateIpIdsReordered" {
  command   = apply
  parallel  = true
  state_key = "private_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix         = "tf-test-server-private-ip"
    subnet_index        = 131
    vpc_id              = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    private_ip_mode     = "both"
    reverse_private_ips = true
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_singleSubnetAttachment_privateIpAllocated.server_id &&
      length(viettelcloud_server.server.private_ips) == 2 &&
      viettelcloud_server.server.private_ips[0].id == run.attachSecondSubnet_oneSubnetAttachment_twoPrivateIpsWithFirstPreserved.private_ip_ids[1] &&
      viettelcloud_server.server.private_ips[1].id == run.attachSecondSubnet_oneSubnetAttachment_twoPrivateIpsWithFirstPreserved.private_ip_ids[0]
    )
    error_message = "Reordering the attachment list reallocated private IPs instead of reordering state."
  }
}

run "removeFirstSubnet_twoSubnetAttachments_secondPrivateIpPreserved" {
  command   = apply
  parallel  = true
  state_key = "private_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix     = "tf-test-server-private-ip"
    subnet_index    = 131
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    private_ip_mode = "second"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_singleSubnetAttachment_privateIpAllocated.server_id &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      viettelcloud_server.server.private_ips[0].id == run.attachSecondSubnet_oneSubnetAttachment_twoPrivateIpsWithFirstPreserved.private_ip_ids[1]
    )
    error_message = "Removing the first subnet attachment detached the wrong private IP."
  }
}

run "returnToFirstSubnet_secondSubnetAttachment_privateIpReallocated" {
  command   = apply
  parallel  = true
  state_key = "private_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    keep_second_subnet = true
    name_prefix        = "tf-test-server-private-ip"
    subnet_index       = 131
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_singleSubnetAttachment_privateIpAllocated.server_id &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      viettelcloud_server.server.private_ips[0].id != run.removeFirstSubnet_twoSubnetAttachments_secondPrivateIpPreserved.private_ip_id &&
      length(viettelcloud_server.server.private_ips[0].ip_address) > 0
    )
    error_message = "Returning to the first subnet did not reallocate and clean up the private IP."
  }
}

run "deleteSubnet_detachedSecondSubnet_subnetRemovedAndServerPreserved" {
  command   = apply
  parallel  = true
  state_key = "private_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    keep_second_subnet = false
    name_prefix        = "tf-test-server-private-ip"
    subnet_index       = 131
    vpc_id             = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition     = length(viettelcloud_subnet.second) == 0 && viettelcloud_server.server.id == run.returnToFirstSubnet_secondSubnetAttachment_privateIpReallocated.server_id && viettelcloud_server.server.private_ips[0].id == run.returnToFirstSubnet_secondSubnetAttachment_privateIpReallocated.private_ip_id
    error_message = "The detached private IP leaked or deleting its subnet changed the server."
  }
}


# Chain existing_ip.

run "createServer_singleSubnetIpBeforeManagedIpAttach_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "existing_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-existing-ip"
    subnet_index = 141
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The existing_ip chain's prerequisite server was not created."
  }
}

run "attachManagedPrivateIp_serverWithSubnetIp_twoAttachmentsWithManagedIpPreserved" {
  command   = apply
  parallel  = true
  state_key = "existing_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix               = "tf-test-server-existing-ip"
    subnet_index              = 141
    vpc_id                    = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    create_managed_private_ip = true
    attach_managed_private_ip = true
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_singleSubnetIpBeforeManagedIpAttach_serverCreated.server_id &&
      length(viettelcloud_server.server.private_ips) == 2 &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_singleSubnetIpBeforeManagedIpAttach_serverCreated.private_ip_id &&
      viettelcloud_server.server.private_ips[1].id == viettelcloud_private_ip.managed[0].id &&
      viettelcloud_server.server.private_ips[1].kind == "ip" &&
      viettelcloud_server.server.private_ips[1].delete_on_termination == false
    )
    error_message = "Attaching an existing private IP by ID did not extend the attachment list."
  }
}

run "detachManagedPrivateIp_managedIpAttachment_managedIpPreserved" {
  command   = apply
  parallel  = true
  state_key = "existing_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix               = "tf-test-server-existing-ip"
    subnet_index              = 141
    vpc_id                    = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    create_managed_private_ip = true
    attach_managed_private_ip = false
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_singleSubnetIpBeforeManagedIpAttach_serverCreated.server_id &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      viettelcloud_server.server.private_ips[0].id == run.attachManagedPrivateIp_serverWithSubnetIp_twoAttachmentsWithManagedIpPreserved.private_ip_id &&
      viettelcloud_private_ip.managed[0].id == run.attachManagedPrivateIp_serverWithSubnetIp_twoAttachmentsWithManagedIpPreserved.managed_private_ip_id
    )
    error_message = "Detaching a practitioner-owned private IP removed the wrong attachment or deleted the IP."
  }
}


# Chain elastic_ip.

run "createServer_noElasticIpConfig_serverRunningWithoutElasticIps" {
  command   = apply
  parallel  = true
  state_key = "elastic_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-elastic-ip"
    subnet_index = 151
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition = (
      length(viettelcloud_server.server.id) > 0 &&
      viettelcloud_server.server.power_state == "running" &&
      length(viettelcloud_server.server.elastic_ips) == 0 &&
      length(viettelcloud_server.server.private_ips) == 1
    )
    error_message = "Server was not created running and without an elastic IP."
  }
}

run "attachElasticIp_serverWithoutElasticIps_allocatedAddressReturned" {
  command   = apply
  parallel  = true
  state_key = "elastic_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-elastic-ip"
    subnet_index     = 151
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    elastic_ip_count = 1
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_noElasticIpConfig_serverRunningWithoutElasticIps.server_id &&
      length(viettelcloud_server.server.elastic_ips) == 1 &&
      viettelcloud_server.server.elastic_ips[0].kind == "new" &&
      viettelcloud_server.server.elastic_ips[0].delete_on_termination &&
      length(viettelcloud_server.server.elastic_ips[0].id) > 0 &&
      length(viettelcloud_server.server.elastic_ips[0].ip_address) > 0 &&
      length(viettelcloud_server.server.elastic_ips[0].status) > 0 &&
      length(viettelcloud_server.server.private_ips) == 1
    )
    error_message = "Attaching a new elastic IP did not expose its allocated address."
  }
}

run "attachSecondElasticIp_oneElasticIpAttachment_firstAttachmentPreserved" {
  command   = apply
  parallel  = true
  state_key = "elastic_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-elastic-ip"
    subnet_index     = 151
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    elastic_ip_count = 2
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_noElasticIpConfig_serverRunningWithoutElasticIps.server_id &&
      length(viettelcloud_server.server.elastic_ips) == 2 &&
      viettelcloud_server.server.elastic_ips[0].id == run.attachElasticIp_serverWithoutElasticIps_allocatedAddressReturned.elastic_ip_ids[0] &&
      length(viettelcloud_server.server.elastic_ips[1].ip_address) > 0
    )
    error_message = "Adding a second elastic IP did not preserve the first attachment."
  }
}

# Non-empty IP lists exercise omission. Key pair and placement group coverage
# requires their optional test inputs. Security groups include backend defaults.
run "planAgain_referencesAndAttachmentsOmitted_stateStable" {
  command   = plan
  parallel  = true
  state_key = "elastic_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix               = "tf-test-server-elastic-ip"
    subnet_index              = 151
    vpc_id                    = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    elastic_ip_count          = 2
    omit_preserved_references = true
  }

  assert {
    condition = (
      output.server_references == run.attachSecondElasticIp_oneElasticIpAttachment_firstAttachmentPreserved.server_references &&
      viettelcloud_server.server.id == run.attachSecondElasticIp_oneElasticIpAttachment_firstAttachmentPreserved.server_id
    )
    error_message = "Omitting references or attachments proposed a change to the server."
  }
}


run "removeSecondElasticIp_twoElasticIpAttachments_firstAttachmentPreserved" {
  command   = apply
  parallel  = true
  state_key = "elastic_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-elastic-ip"
    subnet_index     = 151
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    elastic_ip_count = 1
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_noElasticIpConfig_serverRunningWithoutElasticIps.server_id &&
      length(viettelcloud_server.server.elastic_ips) == 1 &&
      viettelcloud_server.server.elastic_ips[0].id == run.attachSecondElasticIp_oneElasticIpAttachment_firstAttachmentPreserved.elastic_ip_ids[0]
    )
    error_message = "Removing one elastic IP detached the wrong attachment."
  }
}

run "setEmptyElasticIpList_twoElasticIpAttachments_elasticIpsDetachedAndPrivateIpPreserved" {
  command   = apply
  parallel  = true
  state_key = "elastic_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-elastic-ip"
    subnet_index     = 151
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    elastic_ip_count = 0
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_noElasticIpConfig_serverRunningWithoutElasticIps.server_id &&
      length(viettelcloud_server.server.elastic_ips) == 0 &&
      length(viettelcloud_server.server.private_ips) == 1
    )
    error_message = "An empty elastic IP list did not detach every elastic IP."
  }
}

# Teardown destroys the server while it holds this server-owned elastic IP, so
# a teardown that succeeds covers its deletion on destroy. A rejected delete
# fails the teardown.
run "attachElasticIpBeforeTeardown_noElasticIpAttachments_ownedElasticIpDeletedWithServer" {
  command   = apply
  parallel  = true
  state_key = "elastic_ip"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-elastic-ip"
    subnet_index     = 151
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    elastic_ip_count = 1
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_noElasticIpConfig_serverRunningWithoutElasticIps.server_id &&
      length(viettelcloud_server.server.elastic_ips) == 1 &&
      viettelcloud_server.server.elastic_ips[0].kind == "new" &&
      viettelcloud_server.server.elastic_ips[0].delete_on_termination &&
      length(viettelcloud_server.server.elastic_ips[0].id) > 0
    )
    error_message = "The server did not hold a server-owned elastic IP before teardown."
  }
}


# Chain power.

run "createServer_powerStateOmitted_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "power"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-power"
    subnet_index = 161
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The power chain's prerequisite server was not created."
  }
}

run "requestShutdown_runningServer_serverStoppedInPlace" {
  command   = apply
  parallel  = true
  state_key = "power"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-power"
    subnet_index = 161
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    power_state  = "shutdown"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_powerStateOmitted_serverCreated.server_id &&
      viettelcloud_server.server.power_state == "shutdown"
    )
    error_message = "Requesting the shutdown power state did not stop the server in place."
  }
}

run "planAgain_stoppedServer_powerStateStable" {
  command   = plan
  parallel  = true
  state_key = "power"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-power"
    subnet_index = 161
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    power_state  = "shutdown"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_powerStateOmitted_serverCreated.server_id &&
      viettelcloud_server.server.power_state == "shutdown"
    )
    error_message = "Replanning a stopped server reported power state drift."
  }
}

run "requestRunning_stoppedServer_serverStartedInPlace" {
  command   = apply
  parallel  = true
  state_key = "power"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-power"
    subnet_index = 161
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    power_state  = "running"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_powerStateOmitted_serverCreated.server_id &&
      viettelcloud_server.server.power_state == "running" &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x"
    )
    error_message = "Requesting the running power state did not start the server in place."
  }
}


# Chain rebuild.

run "createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned" {
  command   = apply
  parallel  = true
  state_key = "rebuild"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-rebuild"
    subnet_index = 171
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition = (
      length(viettelcloud_server.server.id) > 0 &&
      viettelcloud_server.server.boot.image.name == "cirros" &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x" &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      length(viettelcloud_server.server.data_volume_ids) == 1
    )
    error_message = "Server was not created with the expected boot image and predefined flavor."
  }
}

run "changeImage_existingBootImage_serverRebuiltInPlace" {
  command   = apply
  parallel  = true
  state_key = "rebuild"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-rebuild"
    subnet_index = 171
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image        = "ubuntu-20.04-publiccloud"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned.server_id &&
      viettelcloud_server.server.boot.image.name == "ubuntu-20.04-publiccloud" &&
      viettelcloud_server.server.power_state == "running" &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned.private_ip_id
    )
    error_message = "Changing the boot image did not rebuild the server in place."
  }
}

run "restoreOriginalImage_rebuiltServer_serverRebuiltInPlace" {
  command   = apply
  parallel  = true
  state_key = "rebuild"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-rebuild"
    subnet_index = 171
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image        = "cirros"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned.server_id &&
      viettelcloud_server.server.boot.image.name == "cirros" &&
      viettelcloud_server.server.power_state == "running" &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned.private_ip_id
    )
    error_message = "Rebuilding back to the original image did not succeed in place."
  }
}

run "rebuildAndResizeRunningServer_newFlavorAndImage_rebuiltAndResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "rebuild"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-rebuild"
    subnet_index = 171
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image        = "ubuntu-20.04-publiccloud"
    flavor_kind  = "custom"
    flavor_vcpus = 2
    flavor_ram   = 4
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned.server_id &&
      viettelcloud_server.server.boot.image.name == "ubuntu-20.04-publiccloud" &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.vcpus == 2 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "running" &&
      viettelcloud_server.server.private_ips[0].id == run.createServer_predefinedFlavorAndBootImage_configuredFlavorAndImageReturned.private_ip_id
    )
    error_message = "Simultaneous rebuild and resize on a running server did not update both boot image and flavor in place."
  }
}


# Chain user_data.

run "createServer_userDataOmitted_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "user_data"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-user-data"
    subnet_index = 181
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The user_data chain's prerequisite server was not created."
  }
}

run "configureUserData_serverWithoutUserData_serverAndPrivateIpReplaced" {
  command   = apply
  parallel  = true
  state_key = "user_data"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-user-data"
    subnet_index = 181
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data    = "#cloud-config\npackage_update: true\n"
  }

  assert {
    condition = (
      viettelcloud_server.server.id != run.createServer_userDataOmitted_serverCreated.server_id &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      viettelcloud_server.server.private_ips[0].id != run.createServer_userDataOmitted_serverCreated.private_ip_id &&
      length(viettelcloud_server.server.data_volume_ids) == 1
    )
    error_message = "Configuring user data did not replace the server."
  }
}

run "planAgain_configuredUserData_serverIdentityPreserved" {
  command   = plan
  parallel  = true
  state_key = "user_data"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-user-data"
    subnet_index = 181
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data    = "#cloud-config\npackage_update: true\n"
  }

  assert {
    condition     = viettelcloud_server.server.id == run.configureUserData_serverWithoutUserData_serverAndPrivateIpReplaced.server_id
    error_message = "Configured user data planned another replacement instead of settling."
  }
}


# Chain data_volume.

run "createServer_dataVolumeSize20_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "data_volume"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    user_data               = "#cloud-config\npackage_update: true\n"
    name_prefix             = "tf-test-server-data-volume"
    subnet_index            = 191
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    second_data_volume_size = 20
  }

  # Two data volumes of the same size: a single-volume list cannot show that
  # each element resolved to its own backend volume.
  assert {
    condition = (
      length(viettelcloud_server.server.id) > 0 &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      length(viettelcloud_server.server.data_volume_ids) == 2 &&
      viettelcloud_server.server.data_volumes[0].volume_size == 20 &&
      viettelcloud_server.server.data_volumes[1].volume_size == 20 &&
      length(viettelcloud_server.server.data_volumes[0].id) > 0 &&
      viettelcloud_server.server.data_volumes[0].id != viettelcloud_server.server.data_volumes[1].id
    )
    error_message = "The data_volume chain's prerequisite server was not created with two distinct data volumes."
  }
}

# Boot alone. Every data volume keeping its size proves a boot resize does not
# reach the data volumes sharing the same update. The backend can recalculate
# unconfigured boot IOPS during the resize, so the apply must accept that value.
run "increaseBootVolumeSize_existingServer_onlyBootVolumeResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "data_volume"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix             = "tf-test-server-data-volume"
    subnet_index            = 191
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data               = "#cloud-config\npackage_update: true\n"
    boot_volume_size        = 40
    second_data_volume_size = 20
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_dataVolumeSize20_serverCreated.server_id &&
      viettelcloud_server.server.boot.volume_size == 40 &&
      viettelcloud_server.server.boot.iops != null &&
      viettelcloud_server.server.data_volumes[0].volume_size == 20 &&
      viettelcloud_server.server.data_volumes[1].volume_size == 20 &&
      viettelcloud_server.server.data_volumes[*].id == run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids
    )
    error_message = "Increasing only the boot volume size replaced the server or changed a data volume."
  }
}

# One data volume alone. data_volumes[0] keeping both its ID and its size
# proves the resize followed data_volumes[1].id rather than a position in
# whatever order the server reported its attachments, and the boot volume
# holding at 40 proves a data resize does not reach it.
run "increaseDataVolumeSize_existingServer_onlySecondDataVolumeResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "data_volume"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix             = "tf-test-server-data-volume"
    subnet_index            = 191
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data               = "#cloud-config\npackage_update: true\n"
    data_volume_id          = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[0]
    second_data_volume_id   = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1]
    boot_volume_size        = 40
    second_data_volume_size = 30
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_dataVolumeSize20_serverCreated.server_id &&
      viettelcloud_server.server.boot.volume_size == 40 &&
      viettelcloud_server.server.data_volumes[0].volume_size == 20 &&
      viettelcloud_server.server.data_volumes[1].volume_size == 30 &&
      viettelcloud_server.server.data_volumes[*].id == run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids
    )
    error_message = "Increasing only the second data volume size replaced the server or resized the wrong volume."
  }
}

# Boot and both data volumes in one apply, which is the case that orders the
# extends against each other rather than one at a time.
run "increaseBootAndDataVolumeSizes_existingServer_allVolumesResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "data_volume"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix             = "tf-test-server-data-volume"
    subnet_index            = 191
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data               = "#cloud-config\npackage_update: true\n"
    data_volume_id          = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[0]
    second_data_volume_id   = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1]
    boot_volume_size        = 50
    data_volume_size        = 30
    second_data_volume_size = 40
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_dataVolumeSize20_serverCreated.server_id &&
      viettelcloud_server.server.boot.volume_size == 50 &&
      viettelcloud_server.server.data_volumes[0].volume_size == 30 &&
      viettelcloud_server.server.data_volumes[1].volume_size == 40 &&
      viettelcloud_server.server.data_volumes[*].id == run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids
    )
    error_message = "Increasing the boot and both data volume sizes together replaced the server or missed a volume."
  }
}

run "clearDeleteOnTermination_volumesDeletedOnTermination_volumesKeptInPlace" {
  command   = apply
  parallel  = true
  state_key = "data_volume"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix                       = "tf-test-server-data-volume"
    subnet_index                      = 191
    vpc_id                            = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data                         = "#cloud-config\npackage_update: true\n"
    data_volume_id                    = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[0]
    second_data_volume_id             = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1]
    boot_volume_size                  = 50
    data_volume_size                  = 30
    second_data_volume_size           = 40
    boot_delete_on_termination        = false
    data_volume_delete_on_termination = false
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_dataVolumeSize20_serverCreated.server_id &&
      viettelcloud_server.server.data_volume_ids == run.createServer_dataVolumeSize20_serverCreated.data_volume_ids &&
      viettelcloud_server.server.boot.delete_on_termination == false &&
      viettelcloud_server.server.data_volumes[0].delete_on_termination == false &&
      viettelcloud_server.server.data_volumes[1].delete_on_termination == false
    )
    error_message = "Opting the boot and data volumes out of deletion did not update the server in place."
  }
}

# delete_on_termination is optional and computed, so removing it from
# configuration preserves the last applied value instead of returning to the
# default. A practitioner who wants the volumes deleted again sets it back.
run "omitDeleteOnTermination_volumesKeptOnTermination_keptValuePreserved" {
  command   = apply
  parallel  = true
  state_key = "data_volume"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix             = "tf-test-server-data-volume"
    subnet_index            = 191
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data               = "#cloud-config\npackage_update: true\n"
    data_volume_id          = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[0]
    second_data_volume_id   = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1]
    boot_volume_size        = 50
    data_volume_size        = 30
    second_data_volume_size = 40
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_dataVolumeSize20_serverCreated.server_id &&
      viettelcloud_server.server.boot.delete_on_termination == false &&
      viettelcloud_server.server.data_volumes[0].delete_on_termination == false &&
      viettelcloud_server.server.data_volumes[1].delete_on_termination == false
    )
    error_message = "Omitting delete_on_termination did not preserve the configured value."
  }
}

# Restores the default behavior so teardown deletes both volumes with the
# server instead of leaving them behind in the test project.
run "restoreDeleteOnTermination_volumesKeptOnTermination_volumesDeletedInPlace" {
  command   = apply
  parallel  = true
  state_key = "data_volume"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix                       = "tf-test-server-data-volume"
    subnet_index                      = 191
    vpc_id                            = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data                         = "#cloud-config\npackage_update: true\n"
    data_volume_id                    = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[0]
    second_data_volume_id             = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1]
    boot_volume_size                  = 50
    data_volume_size                  = 30
    second_data_volume_size           = 40
    boot_delete_on_termination        = true
    data_volume_delete_on_termination = true
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_dataVolumeSize20_serverCreated.server_id &&
      viettelcloud_server.server.boot.delete_on_termination &&
      viettelcloud_server.server.data_volumes[0].delete_on_termination &&
      viettelcloud_server.server.data_volumes[1].delete_on_termination
    )
    error_message = "Opting the boot and data volumes back into deletion did not update the server in place."
  }
}


# Explicit IDs identify the retained volume when removal and resize share an
# apply. Offline tests prove that an ambiguous request stops before mutation.
run "removeAndResizeVolumes_twoOwnedVolumes_retainedVolumeIdPreserved" {
  command   = apply
  parallel  = true
  state_key = "data_volume"
  module { source = "./fixtures/server/resource" }
  variables {
    name_prefix             = "tf-test-server-data-volume"
    subnet_index            = 191
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data               = "#cloud-config\npackage_update: true\n"
    boot_volume_size        = 50
    include_data_volume     = false
    second_data_volume_id   = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1]
    second_data_volume_size = 60
  }
  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_dataVolumeSize20_serverCreated.server_id &&
      length(viettelcloud_server.server.data_volumes) == 1 &&
      viettelcloud_server.server.data_volumes[0].id == run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1] &&
      viettelcloud_server.server.data_volumes[0].volume_size == 60 &&
      viettelcloud_server.server.data_volumes[0].delete_on_termination
    )
    error_message = "Removing and resizing volumes changed the retained volume ID or replaced the server."
  }
}
run "planAgain_removedAndResizedVolumes_noDrift" {
  command   = plan
  parallel  = true
  state_key = "data_volume"
  module { source = "./fixtures/server/resource" }
  variables {
    name_prefix             = "tf-test-server-data-volume"
    subnet_index            = 191
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    user_data               = "#cloud-config\npackage_update: true\n"
    boot_volume_size        = 50
    include_data_volume     = false
    second_data_volume_id   = run.createServer_dataVolumeSize20_serverCreated.data_volume_element_ids[1]
    second_data_volume_size = 60
  }
  assert {
    condition     = viettelcloud_server.server.data_volumes == run.removeAndResizeVolumes_twoOwnedVolumes_retainedVolumeIdPreserved.data_volume_state
    error_message = "A repeated plan changed the retained volume after removal and resize."
  }
}


# Chain to_custom.

run "createServer_predefinedFlavorForCustomSwitch_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "to_custom"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    data_volume_size = 30
    user_data        = "#cloud-config\npackage_update: true\n"
    name_prefix      = "tf-test-server-to-custom"
    subnet_index     = 201
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The to_custom chain's prerequisite server was not created."
  }
}

run "switchToCustom_predefinedFlavor_serverResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "to_custom"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-to-custom"
    subnet_index     = 201
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    flavor_kind      = "custom"
    flavor_vcpus     = 2
    flavor_ram       = 4
    user_data        = "#cloud-config\npackage_update: true\n"
    data_volume_size = 30
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorForCustomSwitch_serverCreated.server_id &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.name == null &&
      viettelcloud_server.server.flavor.vcpus == 2 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "running"
    )
    error_message = "Switching to a custom flavor did not resize the server in place."
  }
}


# Chain custom_resize.

run "createServer_predefinedFlavorForCapacityResize_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "custom_resize"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    flavor_ram       = 4
    flavor_vcpus     = 2
    flavor_kind      = "predefined"
    data_volume_size = 30
    user_data        = "#cloud-config\npackage_update: true\n"
    name_prefix      = "tf-test-server-custom-resize"
    subnet_index     = 211
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The custom_resize chain's prerequisite server was not created."
  }
}

# Reach the prior custom capacity through the original predefined-to-custom lifecycle.
run "switchToCustom_predefinedFlavorForCapacityResize_serverResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "custom_resize"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-custom-resize"
    subnet_index     = 211
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    flavor_kind      = "custom"
    flavor_vcpus     = 2
    flavor_ram       = 4
    user_data        = "#cloud-config\npackage_update: true\n"
    data_volume_size = 30
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorForCapacityResize_serverCreated.server_id &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.name == null &&
      viettelcloud_server.server.flavor.vcpus == 2 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "running"
    )
    error_message = "Switching to a custom flavor did not resize the server in place."
  }
}

run "changeCapacity_customFlavor_serverResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "custom_resize"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-custom-resize"
    subnet_index     = 211
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    flavor_kind      = "custom"
    flavor_vcpus     = 4
    flavor_ram       = 4
    user_data        = "#cloud-config\npackage_update: true\n"
    data_volume_size = 30
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorForCapacityResize_serverCreated.server_id &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.name == null &&
      viettelcloud_server.server.flavor.vcpus == 4 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "running"
    )
    error_message = "Resizing custom flavor capacity did not update the server in place."
  }
}


# Chain to_predefined.

run "createServer_predefinedFlavorForRoundTripResize_serverCreated" {
  command   = apply
  parallel  = true
  state_key = "to_predefined"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    flavor_ram       = 4
    flavor_vcpus     = 4
    flavor_kind      = "predefined"
    data_volume_size = 30
    user_data        = "#cloud-config\npackage_update: true\n"
    name_prefix      = "tf-test-server-to-predefined"
    subnet_index     = 221
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
  }

  assert {
    condition     = length(viettelcloud_server.server.id) > 0 && length(viettelcloud_server.server.private_ips) == 1 && length(viettelcloud_server.server.data_volume_ids) == 1
    error_message = "The to_predefined chain's prerequisite server was not created."
  }
}

# Reach the prior custom capacity through the original predefined-to-custom lifecycle.
run "switchToCustom_predefinedFlavorForRoundTripResize_serverResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "to_predefined"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-to-predefined"
    subnet_index     = 221
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    flavor_kind      = "custom"
    flavor_vcpus     = 2
    flavor_ram       = 4
    user_data        = "#cloud-config\npackage_update: true\n"
    data_volume_size = 30
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorForRoundTripResize_serverCreated.server_id &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.name == null &&
      viettelcloud_server.server.flavor.vcpus == 2 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "running"
    )
    error_message = "Switching to a custom flavor did not resize the server in place."
  }
}

run "changeCapacity_customFlavorBeforePredefinedSwitch_serverResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "to_predefined"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix      = "tf-test-server-to-predefined"
    subnet_index     = 221
    vpc_id           = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    flavor_kind      = "custom"
    flavor_vcpus     = 4
    flavor_ram       = 4
    user_data        = "#cloud-config\npackage_update: true\n"
    data_volume_size = 30
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorForRoundTripResize_serverCreated.server_id &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.name == null &&
      viettelcloud_server.server.flavor.vcpus == 4 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "running"
    )
    error_message = "Resizing custom flavor capacity did not update the server in place."
  }
}

run "switchToPredefined_resizedCustomFlavor_serverResizedInPlace" {
  command   = apply
  parallel  = true
  state_key = "to_predefined"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix            = "tf-test-server-to-predefined"
    subnet_index           = 221
    vpc_id                 = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    flavor_kind            = "predefined"
    predefined_flavor_name = "b1.micro2x"
    user_data              = "#cloud-config\npackage_update: true\n"
    data_volume_size       = 30
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_predefinedFlavorForRoundTripResize_serverCreated.server_id &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x" &&
      viettelcloud_server.server.flavor.vcpus == null &&
      viettelcloud_server.server.flavor.ram == null &&
      viettelcloud_server.server.power_state == "running"
    )
    error_message = "Switching back to predefined flavor did not resize the server in place."
  }
}


# Chain import.

run "createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-import"
    subnet_index = 231
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    server_name  = "tf-test-server-import"
  }

  assert {
    condition = (
      length(viettelcloud_server.server.id) > 0 &&
      length(viettelcloud_server.server.data_volume_ids) == 1 &&
      length(viettelcloud_server.server.private_ips) == 1
    )
    error_message = "The import chain's server was not created with a data volume and a private IP."
  }
}

run "planImport_existingServerId_runtimeAndAttachmentStateRestored" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/server/import"
  }

  variables {
    server_id   = run.createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned.server_id
    server_name = "tf-test-server-import"
    data_volumes = [for id in run.createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned.data_volume_ids : {
      kind = "existing"
      id   = id
    }]
  }

  assert {
    condition = (
      viettelcloud_server.imported.id == run.createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned.server_id &&
      viettelcloud_server.imported.name == "tf-test-server-import" &&
      viettelcloud_server.imported.zone.name == "vn-central-1a" &&
      viettelcloud_server.imported.quantity == 1 &&
      viettelcloud_server.imported.power_state == "running" &&
      length(viettelcloud_server.imported.status) > 0 &&
      length(viettelcloud_server.imported.server_type) > 0 &&
      length(viettelcloud_server.imported.created_at) > 0 &&
      viettelcloud_server.imported.flavor.kind == "predefined" &&
      viettelcloud_server.imported.flavor.name == "b1.micro2x" &&
      viettelcloud_server.imported.flavor.vcpus == null &&
      viettelcloud_server.imported.flavor.ram == null &&
      viettelcloud_server.imported.boot.boot_type == "image" &&
      viettelcloud_server.imported.boot.image.name == "cirros" &&
      viettelcloud_server.imported.boot.volume_type.id == run.createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned.data_volume_selectors[0].id &&
      viettelcloud_server.imported.boot.volume_size == 30 &&
      viettelcloud_server.imported.boot.iops != null &&
      viettelcloud_server.imported.data_volume_ids == run.createServer_importFixtureWithVolumeAndPrivateIp_serverWithVolumeAndPrivateIpReturned.data_volume_ids &&
      length(viettelcloud_server.imported.data_volumes) == 1 &&
      viettelcloud_server.imported.data_volumes[0].id == var.data_volumes[0].id &&
      viettelcloud_server.imported.data_volumes[0].kind == "existing" &&
      !viettelcloud_server.imported.data_volumes[0].delete_on_termination &&
      length(viettelcloud_server.imported.private_ips) == 1 &&
      length(viettelcloud_server.imported.private_ips[0].id) > 0 &&
      length(viettelcloud_server.imported.private_ips[0].ip_address) > 0 &&
      length(viettelcloud_server.imported.elastic_ips) == 0
    )
    error_message = "Importing the server did not reproduce its flavor, boot, data volume, and attachment state."
  }
}


# Chain offline_operations.

run "createServer_stoppedServer_serverCreatedShutdown" {
  command   = apply
  parallel  = true
  state_key = "offline_operations"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-offline-ops"
    subnet_index = 241
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    power_state  = "shutdown"
  }

  assert {
    condition = (
      length(viettelcloud_server.server.id) > 0 &&
      viettelcloud_server.server.power_state == "shutdown" &&
      viettelcloud_server.server.boot.image.name == "cirros" &&
      viettelcloud_server.server.flavor.kind == "predefined" &&
      viettelcloud_server.server.flavor.name == "b1.micro2x" &&
      length(viettelcloud_server.server.private_ips) == 1 &&
      length(viettelcloud_server.server.data_volume_ids) == 1
    )
    error_message = "Server was not created in shutdown power state."
  }
}

run "resizeStoppedServer_customFlavor_serverResizedInPlaceWhileShutdown" {
  command   = apply
  parallel  = true
  state_key = "offline_operations"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-offline-ops"
    subnet_index = 241
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    power_state  = "shutdown"
    flavor_kind  = "custom"
    flavor_vcpus = 2
    flavor_ram   = 4
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_stoppedServer_serverCreatedShutdown.server_id &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.name == null &&
      viettelcloud_server.server.flavor.vcpus == 2 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "shutdown"
    )
    error_message = "Offline resize on a stopped server did not update flavor in place while remaining shutdown."
  }
}

run "rebuildStoppedServer_newImage_serverRebuiltInPlaceWhileShutdown" {
  command   = apply
  parallel  = true
  state_key = "offline_operations"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-offline-ops"
    subnet_index = 241
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    power_state  = "shutdown"
    flavor_kind  = "custom"
    flavor_vcpus = 2
    flavor_ram   = 4
    image        = "ubuntu-20.04-publiccloud"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_stoppedServer_serverCreatedShutdown.server_id &&
      viettelcloud_server.server.boot.image.name == "ubuntu-20.04-publiccloud" &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.vcpus == 2 &&
      viettelcloud_server.server.flavor.ram == 4 &&
      viettelcloud_server.server.power_state == "shutdown"
    )
    error_message = "Offline rebuild on a stopped server did not rebuild image in place while remaining shutdown."
  }
}

run "rebuildAndStartServer_stoppedAfterOfflineOperations_serverRebuiltAndStarted" {
  command   = apply
  parallel  = true
  state_key = "offline_operations"

  module {
    source = "./fixtures/server/resource"
  }

  variables {
    name_prefix  = "tf-test-server-offline-ops"
    subnet_index = 241
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    power_state  = "running"
    flavor_kind  = "custom"
    flavor_vcpus = 2
    flavor_ram   = 4
    image        = "cirros"
  }

  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_stoppedServer_serverCreatedShutdown.server_id &&
      viettelcloud_server.server.power_state == "running" &&
      viettelcloud_server.server.boot.image.name == "cirros" &&
      viettelcloud_server.server.flavor.kind == "custom" &&
      viettelcloud_server.server.flavor.vcpus == 2 &&
      viettelcloud_server.server.flavor.ram == 4
    )
    error_message = "Rebuilding and starting the stopped server did not complete in place."
  }
}

# These criteria must identify one catalog entry in the test project.
# Override them when the catalog differs. Each image must fit a 30 GiB volume.
variable "selector_alternate_volume_type" {
  description = "Criteria of a different ready volume type for selector replacement, without encryption or multiple attachments."
  type        = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
  default     = { name = "hdd_io1", encryption_key_size = "0", multiattach = false }
}

variable "selector_alternate_image_name" {
  description = "Name of a different image for selector rebuild."
  type        = string
  default     = "cirros"
}

variable "selector_image" {
  type    = object({ name = optional(string), os = optional(string), version = optional(string), type = optional(string) })
  default = { os = "cirros", version = "20.04" }
}

variable "selector_volume_type" {
  type    = object({ name = optional(string), disk_type = optional(string), speed = optional(string), multiattach = optional(bool), encryption_key_size = optional(string) })
  default = { name = "hdd_st1", encryption_key_size = "0", multiattach = false }
}

run "createServer_catalogCriteria_selectorsPreserved" {
  command   = apply
  parallel  = true
  state_key = "selectors"
  module { source = "./fixtures/server/resource" }
  variables {
    name_prefix          = "tf-test-server-selectors"
    subnet_index         = 251
    vpc_id               = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image_selector       = var.selector_image
    volume_type_selector = var.selector_volume_type
  }
  assert {
    condition     = viettelcloud_server.server.boot.image == merge(var.selector_image, { id = viettelcloud_server.server.boot.image.id }) && viettelcloud_server.server.boot.volume_type == merge(var.selector_volume_type, { id = viettelcloud_server.server.boot.volume_type.id }) && viettelcloud_server.server.data_volumes[0].volume_type == merge(var.selector_volume_type, { id = viettelcloud_server.server.data_volumes[0].volume_type.id })
    error_message = "The server selector lifecycle did not preserve the expected selection or identity."
  }
  assert {
    condition     = viettelcloud_server.server.boot.image.id != null && viettelcloud_server.server.boot.volume_type.id != null && alltrue([for volume in viettelcloud_server.server.data_volumes : volume.volume_type.id != null])
    error_message = "Created selectors must contain catalog IDs."
  }

}

run "planAgain_catalogCriteria_stateStable" {
  command   = plan
  parallel  = true
  state_key = "selectors"
  module { source = "./fixtures/server/resource" }
  variables {
    name_prefix          = "tf-test-server-selectors"
    subnet_index         = 251
    vpc_id               = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image_selector       = var.selector_image
    volume_type_selector = var.selector_volume_type
  }
  assert {
    condition     = viettelcloud_server.server.created_at == run.createServer_catalogCriteria_selectorsPreserved.server_created_at && viettelcloud_server.server.id == run.createServer_catalogCriteria_selectorsPreserved.server_id && viettelcloud_server.server.boot.image == merge(var.selector_image, { id = viettelcloud_server.server.boot.image.id }) && viettelcloud_server.server.boot.volume_type == merge(var.selector_volume_type, { id = viettelcloud_server.server.boot.volume_type.id }) && viettelcloud_server.server.data_volumes[0].volume_type == merge(var.selector_volume_type, { id = viettelcloud_server.server.data_volumes[0].volume_type.id })
    error_message = "The server selector lifecycle did not preserve the expected selection or identity."
  }
}

run "changeDataVolumeCriteria_sameCatalogEntry_serverIdentityPreserved" {
  command   = plan
  parallel  = true
  state_key = "selectors"
  module { source = "./fixtures/server/resource" }
  variables {
    name_prefix               = "tf-test-server-selectors"
    subnet_index              = 251
    vpc_id                    = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image_selector            = var.selector_image
    volume_type_selector      = var.selector_volume_type
    data_volume_type_selector = merge(var.selector_volume_type, { name = " ${var.selector_volume_type.name} " })
  }
  assert {
    condition = (
      viettelcloud_server.server.id == run.createServer_catalogCriteria_selectorsPreserved.server_id &&
      viettelcloud_server.server.data_volumes[0].volume_type.id == run.createServer_catalogCriteria_selectorsPreserved.data_volume_selectors[0].id
    )
    error_message = "Equivalent data-volume criteria must preserve server and type IDs."
  }
}

run "changeBootVolumeTypeSelector_existingSelector_serverReplaced" {
  command   = apply
  parallel  = true
  state_key = "selectors"
  module { source = "./fixtures/server/resource" }
  variables {
    name_prefix               = "tf-test-server-selectors"
    subnet_index              = 251
    vpc_id                    = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image_selector            = var.selector_image
    volume_type_selector      = var.selector_alternate_volume_type
    data_volume_type_selector = var.selector_volume_type
  }
  assert {
    condition     = viettelcloud_server.server.id != run.createServer_catalogCriteria_selectorsPreserved.server_id && viettelcloud_server.server.boot.volume_type == merge(var.selector_alternate_volume_type, { id = viettelcloud_server.server.boot.volume_type.id }) && viettelcloud_server.server.data_volumes[0].volume_type == merge(var.selector_volume_type, { id = viettelcloud_server.server.data_volumes[0].volume_type.id })
    error_message = "The server selector lifecycle did not preserve the expected selection or identity."
  }
}

run "changeImageSelector_existingSelector_serverRebuiltInPlace" {
  command   = apply
  parallel  = true
  state_key = "selectors"
  module { source = "./fixtures/server/resource" }
  variables {
    name_prefix               = "tf-test-server-selectors"
    subnet_index              = 251
    vpc_id                    = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image_selector            = { name = var.selector_alternate_image_name }
    volume_type_selector      = var.selector_alternate_volume_type
    data_volume_type_selector = var.selector_volume_type
  }
  assert {
    condition     = viettelcloud_server.server.id == run.changeBootVolumeTypeSelector_existingSelector_serverReplaced.server_id && viettelcloud_server.server.boot.image.name == var.selector_alternate_image_name
    error_message = "The server selector lifecycle did not preserve the expected selection or identity."
  }
}

# Chain data_volume_attachments.

run "createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image                   = "cirros"
    new_kind                = null
    include_second_existing = true
  }
  assert {
    condition = (
      length(output.server.data_volumes) == 3 &&
      output.server.data_volumes[0].kind == "new" && output.server.data_volumes[0].delete_on_termination &&
      output.server.data_volumes[1].kind == "existing" && !output.server.data_volumes[1].delete_on_termination &&
      output.server.data_volumes[1].id == output.volumes["primary"].id &&
      output.server.data_volumes[2].kind == "existing" && !output.server.data_volumes[2].delete_on_termination &&
      output.server.data_volumes[2].id == output.volumes["replacement"].id &&
      alltrue([for volume in output.server.data_volumes : volume.attached]) &&
      output.volumes["primary"].status == "in-use" && output.volumes["replacement"].status == "in-use" &&
      alltrue([for volume in values(output.volumes) : !volume.encrypted && volume.volume_type == var.volume_type.name]) &&
      alltrue([for volume in output.server.data_volumes : volume.volume_size == 20 && volume.volume_type.name == var.volume_type.name && volume.iops != null])
    )
    error_message = "Mixed volume attachments did not return their complete state and ownership defaults."
  }
}
run "planServer_mixedAttachments_noDrift" {
  command   = plan
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id                  = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image                   = "cirros"
    include_second_existing = true
  }
  assert {
    condition     = output.server.data_volume_ids == run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.data_volume_ids
    error_message = "A repeated plan changed the attached volume IDs."
  }
}
run "detachSecondExistingVolume_multipleCreateAttachments_otherVolumesPreserved" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image  = "cirros"
  }
  assert {
    condition = (
      output.server.id == run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.id &&
      length(output.server.data_volumes) == 2 &&
      output.server.data_volumes[0].id == run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.data_volumes[0].id &&
      output.server.data_volumes[1].id == output.volumes["primary"].id &&
      output.volumes["primary"].status == "in-use" && output.volumes["replacement"].status == "available"
    )
    error_message = "Detaching the second existing volume changed the server or the retained attachments, or deleted a source volume."
  }
}
run "reverseVolumes_mixedAttachments_volumeIdsPreserved" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id        = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image         = "cirros"
    reverse_order = true
  }
  assert {
    condition = (
      output.server.id == run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.id &&
      output.server.data_volumes[1].id == run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.data_volumes[0].id &&
      output.server.data_volumes[0].id == output.volumes["primary"].id
    )
    error_message = "Reordering attachments changed their volume or server IDs."
  }
}
run "replaceExistingVolume_mixedAttachments_serverAndNewVolumePreserved" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image           = "cirros"
    existing_volume = "replacement"
  }
  assert {
    condition = (
      output.server.id == run.reverseVolumes_mixedAttachments_volumeIdsPreserved.server.id &&
      output.server.data_volumes[0].id == run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.data_volumes[0].id &&
      output.server.data_volumes[1].id == output.volumes["replacement"].id &&
      output.volumes["primary"].status == "available" && output.volumes["replacement"].status == "in-use"
    )
    error_message = "Replacing an existing attachment changed the server or the new volume, or deleted a source volume."
  }
}
run "resizeNewVolume_mixedAttachments_volumeExtendedInPlace" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image           = "cirros"
    existing_volume = "replacement"
    new_volume_size = 30
  }
  assert {
    condition = (
      output.server.id == run.replaceExistingVolume_mixedAttachments_serverAndNewVolumePreserved.server.id &&
      output.server.data_volumes[0].id == run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.data_volumes[0].id &&
      output.server.data_volumes[0].volume_size == 30 &&
      output.server.data_volumes[0].iops != null
    )
    error_message = "The new volume did not resize in place."
  }
}
# The backend can recalculate unconfigured IOPS when it extends a volume.
# A repeated plan must preserve the effective value that the resize returned.
run "planServer_resizedVolume_backendIopsPreserved" {
  command   = plan
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image           = "cirros"
    existing_volume = "replacement"
    new_volume_size = 30
  }
  assert {
    condition = (
      output.server.id == run.resizeNewVolume_mixedAttachments_volumeExtendedInPlace.server.id &&
      output.server.data_volumes == run.resizeNewVolume_mixedAttachments_volumeExtendedInPlace.server.data_volumes &&
      output.server.data_volumes[0].iops != null
    )
    error_message = "A repeated plan changed the effective IOPS or volume state after resize."
  }
}
# The following runs detach the first new volume, then create another new volume.
run "removeNewVolume_defaultDeletion_volumeRemovedAndExistingPreserved" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image           = "cirros"
    existing_volume = "replacement"
    include_new     = false
  }
  assert {
    condition = (
      output.server.id == run.resizeNewVolume_mixedAttachments_volumeExtendedInPlace.server.id &&
      length(output.server.data_volumes) == 1 &&
      output.server.data_volumes[0].id == output.volumes["replacement"].id &&
      output.server.data_volumes[0].attached && output.volumes["replacement"].status == "in-use" &&
      output.volumes["primary"].status == "available"
    )
    error_message = "Removing the server-created volume replaced the server."
  }
}
run "addNewVolume_existingAttachment_volumeCreatedWithoutServerReplacement" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image           = "cirros"
    existing_volume = "replacement"
  }
  assert {
    condition = (
      output.server.id == run.resizeNewVolume_mixedAttachments_volumeExtendedInPlace.server.id &&
      length(output.server.data_volumes) == 2 &&
      output.server.data_volumes[0].id != run.createServer_mixedLegacyNewAndExistingVolumes_attachmentDefaultsReturned.server.data_volumes[0].id &&
      output.volumes["replacement"].status == "in-use"
    )
    error_message = "Adding a new volume did not keep the server and existing attachment in place."
  }
}
run "planServer_addedNewVolume_noDrift" {
  command   = plan
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image           = "cirros"
    existing_volume = "replacement"
  }
  assert {
    condition     = output.server.data_volume_ids == run.addNewVolume_existingAttachment_volumeCreatedWithoutServerReplacement.server.data_volume_ids
    error_message = "The newly added volume did not produce a stable plan."
  }
}
run "keepNewVolume_mixedAttachments_deletionFlagUpdatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id                    = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image                     = "cirros"
    existing_volume           = "replacement"
    new_delete_on_termination = false
  }
  assert {
    condition     = output.server.data_volumes[0].id == run.addNewVolume_existingAttachment_volumeCreatedWithoutServerReplacement.server.data_volumes[0].id && !output.server.data_volumes[0].delete_on_termination
    error_message = "Changing the cleanup flag replaced the volume or lost its value."
  }
}
# The rename forces Update while data_volumes remains unconfigured.
# Detached owned volumes and injected deletion errors use offline unit tests.
run "omitVolumes_mixedAttachments_listAndDeletionFlagPreserved" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image        = "cirros"
    omit_volumes = true
    server_name  = "tf-test-server-data-volumes-omitted"
  }
  assert {
    condition = (
      output.server.id == run.keepNewVolume_mixedAttachments_deletionFlagUpdatedInPlace.server.id &&
      output.server.name == "tf-test-server-data-volumes-omitted" &&
      output.server.data_volumes == run.keepNewVolume_mixedAttachments_deletionFlagUpdatedInPlace.server.data_volumes
    )
    error_message = "Omitting data_volumes changed the attachments or their cleanup flags."
  }
}
run "planAgain_volumesOmittedAfterRename_noDrift" {
  command   = plan
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id       = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image        = "cirros"
    omit_volumes = true
    server_name  = "tf-test-server-data-volumes-omitted"
  }
  assert {
    condition     = output.server == run.omitVolumes_mixedAttachments_listAndDeletionFlagPreserved.server
    error_message = "A repeated plan changed the server or its unconfigured data volumes after rename."
  }
}
run "importServer_mixedAttachments_existingOwnershipRestored" {
  command   = plan
  parallel  = true
  state_key = "data_volume_attachments_import"
  module { source = "./fixtures/server/import" }
  variables {
    image       = "cirros"
    server_id   = run.omitVolumes_mixedAttachments_listAndDeletionFlagPreserved.server.id
    server_name = run.omitVolumes_mixedAttachments_listAndDeletionFlagPreserved.server.name
    data_volumes = [for volume in run.omitVolumes_mixedAttachments_listAndDeletionFlagPreserved.server.data_volumes : {
      kind = "existing"
      id   = volume.id
    }]
  }
  assert {
    condition = (
      viettelcloud_server.imported.id == var.server_id && length(viettelcloud_server.imported.data_volumes) == 2 &&
      alltrue([for volume in viettelcloud_server.imported.data_volumes : volume.kind == "existing" && !volume.delete_on_termination && volume.volume_size == 20])
    )
    error_message = "Import did not restore the data attachments with conservative ownership."
  }
}
# The import result reference orders detach after the plan-only import.
run "clearVolumes_mixedAttachments_existingAndKeptNewVolumesAvailable" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments"
  module { source = "./fixtures/server/data_volumes" }
  variables {
    vpc_id          = run.createVpc_isolatedNetwork_validVpcIdReturned.vpc_id
    image           = "cirros"
    include_new     = false
    existing_volume = null
    kept_volume_id  = run.keepNewVolume_mixedAttachments_deletionFlagUpdatedInPlace.server.data_volumes[0].id
  }
  assert {
    condition = (
      output.server.id == run.omitVolumes_mixedAttachments_listAndDeletionFlagPreserved.server.id &&
      output.server.id == run.importServer_mixedAttachments_existingOwnershipRestored.server_id &&
      length(output.server.data_volumes) == 0 && length(output.server.data_volume_ids) == 0 &&
      output.volumes["primary"].status == "available" && output.volumes["replacement"].status == "available" &&
      output.kept_volume.id == var.kept_volume_id && output.kept_volume.status == "available"
    )
    error_message = "Clearing data_volumes changed the server or deleted a retained volume."
  }
}
run "adoptKeptVolume_detachedNewVolume_singleCleanupOwnerEstablished" {
  command   = apply
  parallel  = true
  state_key = "data_volume_attachments_cleanup"
  module { source = "./fixtures/server/detached_volume_cleanup" }
  variables {
    volume_id   = run.clearVolumes_mixedAttachments_existingAndKeptNewVolumesAvailable.kept_volume.id
    volume_name = run.clearVolumes_mixedAttachments_existingAndKeptNewVolumesAvailable.kept_volume.name
    volume_size = run.clearVolumes_mixedAttachments_existingAndKeptNewVolumesAvailable.kept_volume.size
  }
  assert {
    condition     = output.volume.id == var.volume_id && output.volume.status == "available"
    error_message = "The detached volume did not receive its single teardown owner."
  }
}
