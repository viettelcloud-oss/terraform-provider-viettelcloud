# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Every chain adds its rule to the existing L7 policy l7_policy_id, except run
# "changePolicy_secondParent_ruleReplaced", which moves its rule to
# replacement_l7_policy_id. Each chain uses its own value, so the chains
# cannot collide on a duplicate rule.
#
# Chains run one after another because every L7 rule mutation claims the root
# Load Balancer of l7_policy_id, and the backend refuses a concurrent one as
# busy. The provider lock covers one provider process, and each run starts its
# own, so the lock cannot serialize parallel runs.
#
# Chain lifecycle (state_key = "lifecycle"; every mutable field, including
# type, changes in place):
#   run "createRule_pathStartsWithRequiredOnly_completeStateReturned" {
#   run "changeCompareValueAndInvert_pathRule_identityPreserved" {
#   run "omitInvert_invertTrue_falseRestoredInPlace" {
#   run "changeType_pathToHeader_identityPreserved" {
#   run "changeKey_headerRule_identityPreserved" {
#   run "changeType_headerToPath_keyClearedInPlace" {
#   run "planAgain_pathRule_stateStable" {
#
# Chain cookie (state_key = "cookie"; every optional value configured):
#   run "createRule_cookieContainsWithKeyAndInvert_completeStateReturned" {
#
# Chain host_name (state_key = "host_name"):
#   run "createRule_hostNameRegex_completeStateReturned" {
#
# Chain file_type (state_key = "file_type"):
#   run "createRule_fileTypeEqualTo_completeStateReturned" {
#
# Chain replacement (state_key = "replacement"; l7_policy is the only
# attribute that carries a replace plan modifier):
#   run "createRule_replacementChainFixture_ruleCreated" {
#   run "changePolicy_secondParent_ruleReplaced" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of
# the rule this chain creates for itself):
#   run "createRule_importChainFixture_ruleCreated" {
#   run "planImport_existingRule_refreshRestoresComputedValues" {
#
# External deletion is N/A: terraform test cannot delete the rule outside
# Terraform between runs. Unit tests cover the not-found and DELETED refresh.

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
  description = "Existing L7 policy on an HTTP listener of an ACTIVE Load Balancer. Every chain adds its rule here."
  type        = string
}

variable "replacement_l7_policy_id" {
  description = "Second existing L7 policy, other than l7_policy_id. Run changePolicy_secondParent_ruleReplaced moves its rule here."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain lifecycle.

# Creates a PATH rule with only the required attributes and verifies every
# field that the backend returns. key stays null because PATH has no key, and
# invert takes its provider default.
run "createRule_pathStartsWithRequiredOnly_completeStateReturned" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "PATH"
    compare_type = "STARTS_WITH"
    value        = "/tf-test-lifecycle"
  }

  assert {
    condition = (
      length(viettelcloud_lb_l7_rule.rule.id) > 0 &&
      viettelcloud_lb_l7_rule.rule.l7_policy.id == var.l7_policy_id &&
      viettelcloud_lb_l7_rule.rule.type == "PATH" &&
      viettelcloud_lb_l7_rule.rule.compare_type == "STARTS_WITH" &&
      viettelcloud_lb_l7_rule.rule.value == "/tf-test-lifecycle" &&
      viettelcloud_lb_l7_rule.rule.key == null &&
      viettelcloud_lb_l7_rule.rule.invert == false &&
      viettelcloud_lb_l7_rule.rule.provisioning_status == "ACTIVE" &&
      length(viettelcloud_lb_l7_rule.rule.status) > 0 &&
      length(viettelcloud_lb_l7_rule.rule.created_at) > 0 &&
      length(viettelcloud_lb_l7_rule.rule.updated_at) > 0
    )
    error_message = "The L7 rule was not created with the expected values."
  }
}

# Changing compare_type, value, and invert keeps the rule and its identity.
run "changeCompareValueAndInvert_pathRule_identityPreserved" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "PATH"
    compare_type = "EQUAL_TO"
    value        = "/tf-test-lifecycle-v2"
    invert       = true
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.rule.id == run.createRule_pathStartsWithRequiredOnly_completeStateReturned.rule_id &&
      viettelcloud_lb_l7_rule.rule.compare_type == "EQUAL_TO" &&
      viettelcloud_lb_l7_rule.rule.value == "/tf-test-lifecycle-v2" &&
      viettelcloud_lb_l7_rule.rule.invert == true &&
      viettelcloud_lb_l7_rule.rule.provisioning_status == "ACTIVE"
    )
    error_message = "The L7 rule was replaced or its compare_type, value, or invert was not updated."
  }
}

# Omitting invert after true restores the provider default false in place.
run "omitInvert_invertTrue_falseRestoredInPlace" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "PATH"
    compare_type = "EQUAL_TO"
    value        = "/tf-test-lifecycle-v2"
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.rule.id == run.createRule_pathStartsWithRequiredOnly_completeStateReturned.rule_id &&
      viettelcloud_lb_l7_rule.rule.invert == false
    )
    error_message = "Omitting invert replaced the L7 rule or did not restore false."
  }
}

# Changing type to HEADER sends type, compare_type, value, and key in one PATCH
# and keeps the rule.
run "changeType_pathToHeader_identityPreserved" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "HEADER"
    compare_type = "EQUAL_TO"
    key          = "X-Tf-Test-Lifecycle"
    value        = "tf-test-lifecycle"
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.rule.id == run.createRule_pathStartsWithRequiredOnly_completeStateReturned.rule_id &&
      viettelcloud_lb_l7_rule.rule.type == "HEADER" &&
      viettelcloud_lb_l7_rule.rule.key == "X-Tf-Test-Lifecycle" &&
      viettelcloud_lb_l7_rule.rule.value == "tf-test-lifecycle"
    )
    error_message = "Changing type to HEADER replaced the L7 rule or did not apply the key."
  }
}

# Changing the key of a HEADER rule keeps the rule.
run "changeKey_headerRule_identityPreserved" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "HEADER"
    compare_type = "EQUAL_TO"
    key          = "X-Tf-Test-Lifecycle-V2"
    value        = "tf-test-lifecycle"
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.rule.id == run.createRule_pathStartsWithRequiredOnly_completeStateReturned.rule_id &&
      viettelcloud_lb_l7_rule.rule.key == "X-Tf-Test-Lifecycle-V2"
    )
    error_message = "Changing key replaced the L7 rule or did not update the key."
  }
}

# Changing type back to PATH sends a null key, because the backend rejects a
# PATCH that leaves the HEADER key on a PATH rule (Q8 of
# design/loadbalancer/l7_rule.md). This run confirms that the key is cleared.
run "changeType_headerToPath_keyClearedInPlace" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "PATH"
    compare_type = "STARTS_WITH"
    value        = "/tf-test-lifecycle"
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.rule.id == run.createRule_pathStartsWithRequiredOnly_completeStateReturned.rule_id &&
      viettelcloud_lb_l7_rule.rule.type == "PATH" &&
      viettelcloud_lb_l7_rule.rule.key == null
    )
    error_message = "Changing type back to PATH replaced the L7 rule or left the old key."
  }
}

# A plan with the same configuration proposes no change. Any planned change
# would leave updated_at unknown.
run "planAgain_pathRule_stateStable" {
  command   = plan
  parallel  = false
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "PATH"
    compare_type = "STARTS_WITH"
    value        = "/tf-test-lifecycle"
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.rule.id == run.createRule_pathStartsWithRequiredOnly_completeStateReturned.rule_id &&
      viettelcloud_lb_l7_rule.rule.updated_at == run.changeType_headerToPath_keyClearedInPlace.rule_updated_at &&
      viettelcloud_lb_l7_rule.rule.key == null
    )
    error_message = "A repeated plan of the PATH rule proposed a change."
  }
}


# Chain cookie.

# Creates a COOKIE rule with key and invert configured.
run "createRule_cookieContainsWithKeyAndInvert_completeStateReturned" {
  command   = apply
  parallel  = false
  state_key = "cookie"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "COOKIE"
    compare_type = "CONTAINS"
    key          = "tf_test_session"
    value        = "tf-test-cookie"
    invert       = true
  }

  assert {
    condition = (
      length(viettelcloud_lb_l7_rule.rule.id) > 0 &&
      viettelcloud_lb_l7_rule.rule.type == "COOKIE" &&
      viettelcloud_lb_l7_rule.rule.compare_type == "CONTAINS" &&
      viettelcloud_lb_l7_rule.rule.key == "tf_test_session" &&
      viettelcloud_lb_l7_rule.rule.value == "tf-test-cookie" &&
      viettelcloud_lb_l7_rule.rule.invert == true &&
      viettelcloud_lb_l7_rule.rule.provisioning_status == "ACTIVE"
    )
    error_message = "The COOKIE rule was not created with the expected values."
  }
}


# Chain host_name.

run "createRule_hostNameRegex_completeStateReturned" {
  command   = apply
  parallel  = false
  state_key = "host_name"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "HOST_NAME"
    compare_type = "REGEX"
    value        = "^tf-test-host\\.example\\.com$"
  }

  assert {
    condition = (
      length(viettelcloud_lb_l7_rule.rule.id) > 0 &&
      viettelcloud_lb_l7_rule.rule.type == "HOST_NAME" &&
      viettelcloud_lb_l7_rule.rule.compare_type == "REGEX" &&
      viettelcloud_lb_l7_rule.rule.value == "^tf-test-host\\.example\\.com$" &&
      viettelcloud_lb_l7_rule.rule.key == null &&
      viettelcloud_lb_l7_rule.rule.provisioning_status == "ACTIVE"
    )
    error_message = "The HOST_NAME rule was not created with the expected values."
  }
}


# Chain file_type.

run "createRule_fileTypeEqualTo_completeStateReturned" {
  command   = apply
  parallel  = false
  state_key = "file_type"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "FILE_TYPE"
    compare_type = "EQUAL_TO"
    value        = "jpg"
  }

  assert {
    condition = (
      length(viettelcloud_lb_l7_rule.rule.id) > 0 &&
      viettelcloud_lb_l7_rule.rule.type == "FILE_TYPE" &&
      viettelcloud_lb_l7_rule.rule.compare_type == "EQUAL_TO" &&
      viettelcloud_lb_l7_rule.rule.value == "jpg" &&
      viettelcloud_lb_l7_rule.rule.key == null &&
      viettelcloud_lb_l7_rule.rule.provisioning_status == "ACTIVE"
    )
    error_message = "The FILE_TYPE rule was not created with the expected values."
  }
}


# Chain replacement.

run "createRule_replacementChainFixture_ruleCreated" {
  command   = apply
  parallel  = false
  state_key = "replacement"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "PATH"
    compare_type = "STARTS_WITH"
    value        = "/tf-test-replacement"
  }

  assert {
    condition     = length(viettelcloud_lb_l7_rule.rule.id) > 0
    error_message = "The L7 rule of the replacement chain was not created."
  }
}

# Moving the rule to another L7 policy creates a new rule.
run "changePolicy_secondParent_ruleReplaced" {
  command   = apply
  parallel  = false
  state_key = "replacement"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.replacement_l7_policy_id
    type         = "PATH"
    compare_type = "STARTS_WITH"
    value        = "/tf-test-replacement"
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.rule.id != run.createRule_replacementChainFixture_ruleCreated.rule_id &&
      viettelcloud_lb_l7_rule.rule.l7_policy.id == var.replacement_l7_policy_id &&
      viettelcloud_lb_l7_rule.rule.provisioning_status == "ACTIVE"
    )
    error_message = "Changing l7_policy did not replace the L7 rule under the second policy."
  }
}


# Chain import.

# Creates the rule that the import chain imports. invert differs from its
# default, so the import refresh must supply it.
run "createRule_importChainFixture_ruleCreated" {
  command   = apply
  parallel  = false
  state_key = "import"

  module {
    source = "./fixtures/l7_rule/resource"
  }

  variables {
    l7_policy_id = var.l7_policy_id
    type         = "PATH"
    compare_type = "STARTS_WITH"
    value        = "/tf-test-import"
    invert       = true
  }

  assert {
    condition     = length(viettelcloud_lb_l7_rule.rule.id) > 0
    error_message = "The L7 rule of the import chain was not created."
  }
}

# Plans an import of the rule by "<l7_policy_id>/<l7_rule_id>". A planned
# change would leave the computed values unknown, so a passing run proves that
# the refresh restored every configured attribute.
run "planImport_existingRule_refreshRestoresComputedValues" {
  command   = plan
  parallel  = false
  state_key = "import_target"

  module {
    source = "./fixtures/l7_rule/import"
  }

  variables {
    l7_rule_id   = run.createRule_importChainFixture_ruleCreated.rule_id
    l7_policy_id = run.createRule_importChainFixture_ruleCreated.rule_l7_policy_id
    type         = run.createRule_importChainFixture_ruleCreated.rule_type
    compare_type = run.createRule_importChainFixture_ruleCreated.rule_compare_type
    value        = run.createRule_importChainFixture_ruleCreated.rule_value
    invert       = run.createRule_importChainFixture_ruleCreated.rule_invert
  }

  assert {
    condition = (
      viettelcloud_lb_l7_rule.imported.id == run.createRule_importChainFixture_ruleCreated.rule_id &&
      viettelcloud_lb_l7_rule.imported.key == null &&
      viettelcloud_lb_l7_rule.imported.provisioning_status == run.createRule_importChainFixture_ruleCreated.rule_provisioning_status &&
      length(viettelcloud_lb_l7_rule.imported.status) > 0 &&
      viettelcloud_lb_l7_rule.imported.created_at == run.createRule_importChainFixture_ruleCreated.rule_created_at &&
      viettelcloud_lb_l7_rule.imported.updated_at == run.createRule_importChainFixture_ruleCreated.rule_updated_at
    )
    error_message = "The import refresh did not restore the computed values of the L7 rule."
  }
}
