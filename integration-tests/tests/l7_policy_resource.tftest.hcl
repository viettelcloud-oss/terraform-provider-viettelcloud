# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order.
#
# Every chain adds its policies to the existing listener listener_id, except
# run "changeListener_secondParent_policyReplaced", which moves its policy to
# replacement_listener_id. The chains run one after another, not in parallel.
# A policy that another chain creates or deletes on the same listener shifts
# the position of every policy after it. The lifecycle chain configures and
# asserts positions, so a concurrent chain could make them drift.
#
# terraform test skips every run after a failed run, including the runs of
# other state keys. The chains that change no action therefore run first, and
# the action shifts run last. The replacement, import, and external deletion
# chains each delete their policy at the end. The Load Balancer then holds at
# most four policies at one time, and the lifecycle chain starts without them.
#
# Chain replacement (state_key = "replacement"; listener is the only
# attribute that carries a replace plan modifier):
#   run "createPolicy_replacementChainFixture_policyCreated" {
#   run "planAgain_upperCaseListenerID_stateStable" {
#   run "changeListener_secondParent_policyReplaced" {
#   run "destroyPolicy_replacedPolicy_deletionObserved" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of
# the policy this chain creates for itself):
#   run "createPolicy_importChainFixture_policyCreated" {
#   run "planImport_existingPolicy_refreshRestoresComputedValues" {
#   run "destroyPolicy_importedPolicy_deletionObserved" {
#
# Chain external deletion (state_key = "external", with the second owner
# "external_owner"; the second owner imports the policy and deletes it, so the
# deletion happens outside the state that created the policy):
#   run "createPolicy_externalDeletionChainFixture_policyCreated" {
#   run "importPolicy_secondOwner_policyAdopted" {
#   run "deletePolicy_secondOwner_policyDeleted" {
#   run "refreshPolicy_removedOutsideTerraform_stateRemoved" {
#   run "destroyPolicy_recreatedPolicy_deletionObserved" {
#
# Chain lifecycle (state_key = "lifecycle"; four policies p1 to p4 start as
# the four redirect variants. The omission and normalization runs keep every
# action. Three action shifts then move every policy through every ordered
# pair of actions, so the chain covers all twelve transitions):
#   run "createPolicy_eachRedirectVariant_completeStateReturned" {
#   run "omitPolicy_optionalInputs_supportedValuesPreserved" {
#   run "planAgain_omittedInputs_stateStable" {
#   run "padStrings_surroundingWhitespaceAndUpperCaseID_configuredValuesKept" {
#   run "planAgain_paddedStringsAndUpperCaseID_stateStable" {
#   run "changePolicy_actionTargetAndPosition_identityPreserved" {
#   run "changePolicy_secondActionShift_identityPreserved" {
#   run "changePolicy_thirdActionShift_identityPreserved" {
#   run "destroyPolicy_existingSubject_deletionObserved" {

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

variable "listener_id" {
  description = "Existing HTTP or TERMINATED_HTTPS listener of an ACTIVE Load Balancer whose type is not NATIVE. Every chain adds its policies here."
  type        = string
}

variable "replacement_listener_id" {
  description = "Second existing HTTP or TERMINATED_HTTPS listener, other than listener_id. Run changeListener_secondParent_policyReplaced moves its policy here."
  type        = string
}

variable "server_group_id" {
  description = "Existing server group of the Load Balancer that owns listener_id. The REDIRECT_TO_SERVER_GROUP policies redirect here."
  type        = string
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}


# Chain replacement.

# Creates the policy that run "changeListener_secondParent_policyReplaced"
# moves to the second listener. The listener ID is upper case, and the API
# returns it in lower case, so state must keep the configured case.
run "createPolicy_replacementChainFixture_policyCreated" {
  command   = apply
  state_key = "replacement"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = upper(var.listener_id)
    policies = {
      replacement = {
        name   = "tf-test-l7p-replacement"
        action = "REJECT"
      }
    }
  }

  assert {
    condition     = viettelcloud_lb_l7_policy.policy["replacement"].listener.id == upper(var.listener_id)
    error_message = "The replacement chain policy was not created on listener_id, or state did not keep the upper-case listener ID."
  }
}

# A plan with the upper-case listener ID proposes no change. A planned
# replacement makes id unknown, so the condition cannot be evaluated and the
# run fails.
run "planAgain_upperCaseListenerID_stateStable" {
  command   = plan
  state_key = "replacement"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = upper(var.listener_id)
    policies = {
      replacement = {
        name   = "tf-test-l7p-replacement"
        action = "REJECT"
      }
    }
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["replacement"].id == run.createPolicy_replacementChainFixture_policyCreated.policies["replacement"].id &&
      length(viettelcloud_lb_l7_policy.policy["replacement"].updated_at) > 0
    )
    error_message = "The upper-case listener ID planned a change after apply."
  }
}

# Changing the listener replaces the policy, so the ID changes.
run "changeListener_secondParent_policyReplaced" {
  command   = apply
  state_key = "replacement"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.replacement_listener_id
    policies = {
      replacement = {
        name   = "tf-test-l7p-replacement"
        action = "REJECT"
      }
    }
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["replacement"].id != run.createPolicy_replacementChainFixture_policyCreated.policies["replacement"].id &&
      viettelcloud_lb_l7_policy.policy["replacement"].listener.id == var.replacement_listener_id &&
      viettelcloud_lb_l7_policy.policy["replacement"].provisioning_status == "ACTIVE"
    )
    error_message = "Changing listener did not replace the L7 policy on the second listener."
  }
}

# Removing the policy from the configuration deletes it from
# replacement_listener_id. A successful apply observes the deletion, and the
# lifecycle chain starts without this policy on the Load Balancer.
run "destroyPolicy_replacedPolicy_deletionObserved" {
  command   = apply
  state_key = "replacement"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.replacement_listener_id
    policies    = {}
  }

  assert {
    condition     = length(viettelcloud_lb_l7_policy.policy) == 0
    error_message = "The replaced L7 policy remained after its removal from the configuration."
  }
}


# Chain import.

# Creates the policy that run
# "planImport_existingPolicy_refreshRestoresComputedValues" imports. It
# configures a description and a non-default code, so the import refresh must
# read both from the backend.
run "createPolicy_importChainFixture_policyCreated" {
  command   = apply
  state_key = "import"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      import = {
        name               = "tf-test-l7p-import"
        description        = "Import subject"
        action             = "REDIRECT_TO_URL"
        redirect_url       = "https://example.com/import"
        redirect_http_code = 301
      }
    }
  }

  assert {
    condition     = length(viettelcloud_lb_l7_policy.policy["import"].id) > 0
    error_message = "The import chain policy was not created."
  }
}

# Importing the policy restores its complete state from the backend and plans
# no change. The run stays plan-only with state_key "import_target", so run
# "createPolicy_importChainFixture_policyCreated" keeps sole ownership for
# teardown. The inputs come from that run's state. The listener ID is lower
# case because the backend reports UUIDs in lower case.
run "planImport_existingPolicy_refreshRestoresComputedValues" {
  command   = plan
  state_key = "import_target"

  module {
    source = "./fixtures/l7_policy/import"
  }

  variables {
    l7_policy_id = run.createPolicy_importChainFixture_policyCreated.policies["import"].id
    listener_id  = lower(run.createPolicy_importChainFixture_policyCreated.policies["import"].listener.id)
    name         = run.createPolicy_importChainFixture_policyCreated.policies["import"].name
    action       = run.createPolicy_importChainFixture_policyCreated.policies["import"].action
    redirect_url = run.createPolicy_importChainFixture_policyCreated.policies["import"].redirect_url
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.imported.id == run.createPolicy_importChainFixture_policyCreated.policies["import"].id &&
      viettelcloud_lb_l7_policy.imported.name == "tf-test-l7p-import" &&
      viettelcloud_lb_l7_policy.imported.description == "Import subject" &&
      viettelcloud_lb_l7_policy.imported.action == "REDIRECT_TO_URL" &&
      viettelcloud_lb_l7_policy.imported.position == run.createPolicy_importChainFixture_policyCreated.policies["import"].position &&
      viettelcloud_lb_l7_policy.imported.redirect_url == "https://example.com/import" &&
      viettelcloud_lb_l7_policy.imported.redirect_http_code == 301 &&
      viettelcloud_lb_l7_policy.imported.redirect_server_group == null &&
      viettelcloud_lb_l7_policy.imported.redirect_prefix == null &&
      viettelcloud_lb_l7_policy.imported.rule_count == 0 &&
      viettelcloud_lb_l7_policy.imported.provisioning_status == "ACTIVE" &&
      viettelcloud_lb_l7_policy.imported.created_at == run.createPolicy_importChainFixture_policyCreated.policies["import"].created_at &&
      length(viettelcloud_lb_l7_policy.imported.updated_at) > 0
    )
    error_message = "Import did not refresh the complete L7 policy state, or planned a change."
  }
}

# Removing the policy from the configuration deletes it from listener_id
# before the lifecycle chain creates and positions its policies there.
run "destroyPolicy_importedPolicy_deletionObserved" {
  command   = apply
  state_key = "import"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies    = {}
  }

  assert {
    condition     = length(viettelcloud_lb_l7_policy.policy) == 0
    error_message = "The imported L7 policy remained after its removal from the configuration."
  }
}


# Chain external deletion.

# Creates the policy that the second owner deletes.
run "createPolicy_externalDeletionChainFixture_policyCreated" {
  command   = apply
  state_key = "external"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      external = {
        name   = "tf-test-l7p-external"
        action = "REJECT"
      }
    }
  }

  assert {
    condition     = length(viettelcloud_lb_l7_policy.policy["external"].id) > 0
    error_message = "The external deletion chain policy was not created."
  }
}

# A second state imports the policy, so it can delete the policy without the
# state_key "external" knowing.
run "importPolicy_secondOwner_policyAdopted" {
  command   = apply
  state_key = "external_owner"

  module {
    source = "./fixtures/l7_policy/import"
  }

  variables {
    l7_policy_id = run.createPolicy_externalDeletionChainFixture_policyCreated.policies["external"].id
    listener_id  = lower(run.createPolicy_externalDeletionChainFixture_policyCreated.policies["external"].listener.id)
    name         = run.createPolicy_externalDeletionChainFixture_policyCreated.policies["external"].name
    action       = run.createPolicy_externalDeletionChainFixture_policyCreated.policies["external"].action
    redirect_url = null
  }

  assert {
    condition     = viettelcloud_lb_l7_policy.imported.id == run.createPolicy_externalDeletionChainFixture_policyCreated.policies["external"].id
    error_message = "The second owner did not import the external deletion chain policy."
  }
}

# The configuration of the second owner no longer has the imported policy, so
# the apply deletes it.
run "deletePolicy_secondOwner_policyDeleted" {
  command   = apply
  state_key = "external_owner"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies    = {}
  }

  assert {
    condition     = length(viettelcloud_lb_l7_policy.policy) == 0
    error_message = "The second owner did not delete the imported policy."
  }
}

# The refresh reads 404 for the deleted policy and removes it from the state,
# so the apply creates the policy again with a new ID.
run "refreshPolicy_removedOutsideTerraform_stateRemoved" {
  command   = apply
  state_key = "external"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      external = {
        name   = "tf-test-l7p-external"
        action = "REJECT"
      }
    }
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["external"].id != run.createPolicy_externalDeletionChainFixture_policyCreated.policies["external"].id &&
      viettelcloud_lb_l7_policy.policy["external"].provisioning_status == "ACTIVE"
    )
    error_message = "The refresh did not remove the deleted policy, or the apply did not create it again."
  }
}

# Removing the policy from the configuration deletes it from listener_id
# before the lifecycle chain creates and positions its policies there.
run "destroyPolicy_recreatedPolicy_deletionObserved" {
  command   = apply
  state_key = "external"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies    = {}
  }

  assert {
    condition     = length(viettelcloud_lb_l7_policy.policy) == 0
    error_message = "The recreated L7 policy remained after its removal from the configuration."
  }
}


# Chain lifecycle.

# Creates one policy per redirect variant and verifies every field that the
# backend returns. p1 has only the required attributes. p2 omits
# redirect_http_code, so the URL redirect takes the provider default 302. p3
# sets the description that run
# "omitPolicy_optionalInputs_supportedValuesPreserved" clears. p4 configures
# every optional attribute.
run "createPolicy_eachRedirectVariant_completeStateReturned" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name   = "tf-test-l7p-1"
        action = "REJECT"
      }
      p2 = {
        name         = "tf-test-l7p-2"
        action       = "REDIRECT_TO_URL"
        redirect_url = "https://example.com/maintenance"
      }
      p3 = {
        name                     = "tf-test-l7p-3"
        description              = "Server group subject"
        action                   = "REDIRECT_TO_SERVER_GROUP"
        redirect_server_group_id = var.server_group_id
      }
      p4 = {
        name               = "tf-test-l7p-4"
        description        = "Every optional value"
        action             = "REDIRECT_PREFIX"
        position           = 1
        redirect_prefix    = "https://example.com"
        redirect_http_code = 301
      }
    }
  }

  assert {
    condition = alltrue([
      for policy in viettelcloud_lb_l7_policy.policy : (
        length(policy.id) > 0 &&
        policy.listener.id == var.listener_id &&
        policy.position > 0 &&
        policy.rule_count == 0 &&
        policy.provisioning_status == "ACTIVE" &&
        length(policy.status) > 0 &&
        length(policy.created_at) > 0 &&
        length(policy.updated_at) > 0
      )
    ])
    error_message = "An L7 policy was not created with its identity, listener, and computed values."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p1"].name == "tf-test-l7p-1" &&
      viettelcloud_lb_l7_policy.policy["p1"].action == "REJECT" &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_server_group == null &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_url == null &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_prefix == null &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_http_code == null
    )
    error_message = "The REJECT policy with only required values has a target or a code."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p2"].action == "REDIRECT_TO_URL" &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_url == "https://example.com/maintenance" &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_http_code == 302 &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_server_group == null &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_prefix == null
    )
    error_message = "The URL redirect did not use the default code 302 or kept another target."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p3"].description == "Server group subject" &&
      viettelcloud_lb_l7_policy.policy["p3"].action == "REDIRECT_TO_SERVER_GROUP" &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_server_group.id == var.server_group_id &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_url == null &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_prefix == null &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_http_code == null
    )
    error_message = "The server group redirect did not reference server_group_id or kept another target."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p4"].description == "Every optional value" &&
      viettelcloud_lb_l7_policy.policy["p4"].action == "REDIRECT_PREFIX" &&
      viettelcloud_lb_l7_policy.policy["p4"].position == 1 &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_prefix == "https://example.com" &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_http_code == 301 &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_server_group == null &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_url == null
    )
    error_message = "The prefix redirect did not keep every configured optional value."
  }
}

# Omits optional values and clears one description, while p1 also changes its
# name in the same apply. Omitted description, position, and code keep their
# values. An explicit empty description clears p3 to "", not to null. Every
# policy keeps its action, so the run does not depend on an action change.
#
# The run asserts no exact position. A policy created without a position can
# move the policies created before it, so p4 need not stay at position 1. An
# omitted position plans the refreshed value, and a different value after
# apply fails the run as an inconsistent result.
run "omitPolicy_optionalInputs_supportedValuesPreserved" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name   = "tf-test-l7p-1-renamed"
        action = "REJECT"
      }
      p2 = {
        name         = "tf-test-l7p-2"
        action       = "REDIRECT_TO_URL"
        redirect_url = "https://example.com/maintenance"
      }
      p3 = {
        name                     = "tf-test-l7p-3"
        description              = ""
        action                   = "REDIRECT_TO_SERVER_GROUP"
        redirect_server_group_id = var.server_group_id
      }
      p4 = {
        name            = "tf-test-l7p-4"
        action          = "REDIRECT_PREFIX"
        redirect_prefix = "https://example.com"
      }
    }
  }

  assert {
    condition = alltrue([
      for key, policy in viettelcloud_lb_l7_policy.policy :
      policy.id == run.createPolicy_eachRedirectVariant_completeStateReturned.policies[key].id
    ])
    error_message = "Omitting optional values replaced an L7 policy."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p1"].name == "tf-test-l7p-1-renamed" &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_http_code == 302 &&
      viettelcloud_lb_l7_policy.policy["p3"].description == "" &&
      viettelcloud_lb_l7_policy.policy["p4"].description == "Every optional value" &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_http_code == 301 &&
      toset([for policy in viettelcloud_lb_l7_policy.policy : policy.position]) == toset([1, 2, 3, 4])
    )
    error_message = "Omitted values were not preserved, the explicit empty description was not cleared to \"\", or the four policies do not hold positions 1 to 4."
  }
}

# A plan with the same configuration proposes no change. A planned in-place
# update makes updated_at unknown, and a replacement makes id unknown, so the
# condition cannot be evaluated and the run fails in both cases.
run "planAgain_omittedInputs_stateStable" {
  command   = plan
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name   = "tf-test-l7p-1-renamed"
        action = "REJECT"
      }
      p2 = {
        name         = "tf-test-l7p-2"
        action       = "REDIRECT_TO_URL"
        redirect_url = "https://example.com/maintenance"
      }
      p3 = {
        name                     = "tf-test-l7p-3"
        description              = ""
        action                   = "REDIRECT_TO_SERVER_GROUP"
        redirect_server_group_id = var.server_group_id
      }
      p4 = {
        name            = "tf-test-l7p-4"
        action          = "REDIRECT_PREFIX"
        redirect_prefix = "https://example.com"
      }
    }
  }

  assert {
    condition = alltrue([
      for key, policy in viettelcloud_lb_l7_policy.policy :
      policy.id == run.omitPolicy_optionalInputs_supportedValuesPreserved.policies[key].id && length(policy.updated_at) > 0
    ])
    error_message = "A configuration with omitted values planned a change after apply."
  }
}

# Pads name, description, redirect_url, and redirect_prefix with surrounding
# whitespace, and configures the server group ID in upper case. The API trims
# the strings and returns the UUID in lower case, so state must keep each
# configured value. Every policy keeps its action.
run "padStrings_surroundingWhitespaceAndUpperCaseID_configuredValuesKept" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name   = " tf-test-l7p-1-renamed "
        action = "REJECT"
      }
      p2 = {
        name         = "tf-test-l7p-2"
        description  = " Padded description "
        action       = "REDIRECT_TO_URL"
        redirect_url = "https://example.com/maintenance "
      }
      p3 = {
        name                     = "tf-test-l7p-3"
        description              = ""
        action                   = "REDIRECT_TO_SERVER_GROUP"
        redirect_server_group_id = upper(var.server_group_id)
      }
      p4 = {
        name            = "tf-test-l7p-4"
        action          = "REDIRECT_PREFIX"
        redirect_prefix = "https://example.com "
      }
    }
  }

  assert {
    condition = alltrue([
      for key, policy in viettelcloud_lb_l7_policy.policy :
      policy.id == run.createPolicy_eachRedirectVariant_completeStateReturned.policies[key].id && policy.provisioning_status == "ACTIVE"
    ])
    error_message = "A padded value or an upper-case ID replaced an L7 policy."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p1"].name == " tf-test-l7p-1-renamed " &&
      viettelcloud_lb_l7_policy.policy["p2"].description == " Padded description " &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_url == "https://example.com/maintenance " &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_http_code == 302 &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_server_group.id == upper(var.server_group_id) &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_prefix == "https://example.com " &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_http_code == 301
    )
    error_message = "State did not keep a padded string or the upper-case server group ID, or a URL or prefix redirect lost its code."
  }
}

# A plan with the padded strings and the upper-case server group ID proposes
# no change. A planned in-place update makes updated_at unknown, so the
# condition cannot be evaluated and the run fails.
run "planAgain_paddedStringsAndUpperCaseID_stateStable" {
  command   = plan
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name   = " tf-test-l7p-1-renamed "
        action = "REJECT"
      }
      p2 = {
        name         = "tf-test-l7p-2"
        description  = " Padded description "
        action       = "REDIRECT_TO_URL"
        redirect_url = "https://example.com/maintenance "
      }
      p3 = {
        name                     = "tf-test-l7p-3"
        description              = ""
        action                   = "REDIRECT_TO_SERVER_GROUP"
        redirect_server_group_id = upper(var.server_group_id)
      }
      p4 = {
        name            = "tf-test-l7p-4"
        action          = "REDIRECT_PREFIX"
        redirect_prefix = "https://example.com "
      }
    }
  }

  assert {
    condition = alltrue([
      for key, policy in viettelcloud_lb_l7_policy.policy :
      policy.id == run.padStrings_surroundingWhitespaceAndUpperCaseID_configuredValuesKept.policies[key].id && length(policy.updated_at) > 0
    ])
    error_message = "A padded string or the upper-case server group ID planned a change after apply."
  }
}

# First action shift: REJECT -> URL, URL -> server group, server group ->
# prefix, and prefix -> REJECT. p1 also moves to position 2 and p3 changes
# its description. p1 switches from an action without a code and omits the
# code, so it takes 302.
run "changePolicy_actionTargetAndPosition_identityPreserved" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name         = "tf-test-l7p-1-renamed"
        action       = "REDIRECT_TO_URL"
        position     = 2
        redirect_url = "https://example.com/p1"
      }
      p2 = {
        name                     = "tf-test-l7p-2"
        action                   = "REDIRECT_TO_SERVER_GROUP"
        redirect_server_group_id = var.server_group_id
      }
      p3 = {
        name               = "tf-test-l7p-3"
        description        = "Changed description"
        action             = "REDIRECT_PREFIX"
        redirect_prefix    = "http://example.com"
        redirect_http_code = 308
      }
      p4 = {
        name        = "tf-test-l7p-4"
        description = "Every optional value"
        action      = "REJECT"
      }
    }
  }

  assert {
    condition = alltrue([
      for key, policy in viettelcloud_lb_l7_policy.policy :
      policy.id == run.createPolicy_eachRedirectVariant_completeStateReturned.policies[key].id && policy.provisioning_status == "ACTIVE"
    ])
    error_message = "An action change replaced an L7 policy."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p1"].action == "REDIRECT_TO_URL" &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_url == "https://example.com/p1" &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_http_code == 302 &&
      viettelcloud_lb_l7_policy.policy["p1"].position == 2 &&
      viettelcloud_lb_l7_policy.policy["p2"].action == "REDIRECT_TO_SERVER_GROUP" &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_server_group.id == var.server_group_id &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_url == null &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_http_code == null &&
      viettelcloud_lb_l7_policy.policy["p3"].action == "REDIRECT_PREFIX" &&
      viettelcloud_lb_l7_policy.policy["p3"].description == "Changed description" &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_prefix == "http://example.com" &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_http_code == 308 &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_server_group == null &&
      viettelcloud_lb_l7_policy.policy["p4"].action == "REJECT" &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_prefix == null &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_http_code == null
    )
    error_message = "The first action shift did not apply the new variants, clear the old targets, or keep the expected codes and positions."
  }
}

# Second action shift: URL -> prefix, server group -> REJECT, prefix -> URL,
# and REJECT -> server group. p1 and p3 omit the code while they move between
# URL and prefix, so they keep the codes in effect, 302 and 308.
run "changePolicy_secondActionShift_identityPreserved" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name            = "tf-test-l7p-1-renamed"
        action          = "REDIRECT_PREFIX"
        position        = 2
        redirect_prefix = "https://example.com"
      }
      p2 = {
        name   = "tf-test-l7p-2"
        action = "REJECT"
      }
      p3 = {
        name         = "tf-test-l7p-3"
        description  = "Changed description"
        action       = "REDIRECT_TO_URL"
        redirect_url = "https://example.com/p3"
      }
      p4 = {
        name                     = "tf-test-l7p-4"
        description              = "Every optional value"
        action                   = "REDIRECT_TO_SERVER_GROUP"
        redirect_server_group_id = var.server_group_id
      }
    }
  }

  assert {
    condition = alltrue([
      for key, policy in viettelcloud_lb_l7_policy.policy :
      policy.id == run.createPolicy_eachRedirectVariant_completeStateReturned.policies[key].id && policy.provisioning_status == "ACTIVE"
    ])
    error_message = "An action change replaced an L7 policy."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p1"].action == "REDIRECT_PREFIX" &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_prefix == "https://example.com" &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_http_code == 302 &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_url == null &&
      viettelcloud_lb_l7_policy.policy["p2"].action == "REJECT" &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_server_group == null &&
      viettelcloud_lb_l7_policy.policy["p3"].action == "REDIRECT_TO_URL" &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_url == "https://example.com/p3" &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_http_code == 308 &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_prefix == null &&
      viettelcloud_lb_l7_policy.policy["p4"].action == "REDIRECT_TO_SERVER_GROUP" &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_server_group.id == var.server_group_id &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_http_code == null
    )
    error_message = "The second action shift did not apply the new variants, clear the old targets, or keep the codes in effect."
  }
}

# Third action shift: prefix -> server group, REJECT -> prefix, URL -> REJECT,
# and server group -> URL. p2 switches from an action without a code and
# omits the code, so it takes 302. p4 configures 301.
run "changePolicy_thirdActionShift_identityPreserved" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies = {
      p1 = {
        name                     = "tf-test-l7p-1-renamed"
        action                   = "REDIRECT_TO_SERVER_GROUP"
        position                 = 2
        redirect_server_group_id = var.server_group_id
      }
      p2 = {
        name            = "tf-test-l7p-2"
        action          = "REDIRECT_PREFIX"
        redirect_prefix = "https://example.com"
      }
      p3 = {
        name        = "tf-test-l7p-3"
        description = "Changed description"
        action      = "REJECT"
      }
      p4 = {
        name               = "tf-test-l7p-4"
        description        = "Every optional value"
        action             = "REDIRECT_TO_URL"
        redirect_url       = "https://example.com/p4"
        redirect_http_code = 301
      }
    }
  }

  assert {
    condition = alltrue([
      for key, policy in viettelcloud_lb_l7_policy.policy :
      policy.id == run.createPolicy_eachRedirectVariant_completeStateReturned.policies[key].id && policy.provisioning_status == "ACTIVE"
    ])
    error_message = "An action change replaced an L7 policy."
  }

  assert {
    condition = (
      viettelcloud_lb_l7_policy.policy["p1"].action == "REDIRECT_TO_SERVER_GROUP" &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_server_group.id == var.server_group_id &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_prefix == null &&
      viettelcloud_lb_l7_policy.policy["p1"].redirect_http_code == null &&
      viettelcloud_lb_l7_policy.policy["p1"].position == 2 &&
      viettelcloud_lb_l7_policy.policy["p2"].action == "REDIRECT_PREFIX" &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_prefix == "https://example.com" &&
      viettelcloud_lb_l7_policy.policy["p2"].redirect_http_code == 302 &&
      viettelcloud_lb_l7_policy.policy["p3"].action == "REJECT" &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_url == null &&
      viettelcloud_lb_l7_policy.policy["p3"].redirect_http_code == null &&
      viettelcloud_lb_l7_policy.policy["p4"].action == "REDIRECT_TO_URL" &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_url == "https://example.com/p4" &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_http_code == 301 &&
      viettelcloud_lb_l7_policy.policy["p4"].redirect_server_group == null
    )
    error_message = "The third action shift did not apply the new variants, clear the old targets, or keep the expected codes."
  }
}

# Removing every policy from the configuration deletes them. The provider
# returns from Delete only after the backend reports the policy as not found,
# so a successful apply observes each deletion. The listener and the server
# group stay, because the chain does not own them.
run "destroyPolicy_existingSubject_deletionObserved" {
  command   = apply
  state_key = "lifecycle"

  module {
    source = "./fixtures/l7_policy/resource"
  }

  variables {
    listener_id = var.listener_id
    policies    = {}
  }

  assert {
    condition     = length(viettelcloud_lb_l7_policy.policy) == 0
    error_message = "An L7 policy remained after its removal from the configuration."
  }
}
