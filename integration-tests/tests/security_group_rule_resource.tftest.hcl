# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain security_group (shared setup; state_key = "security_group"):
#   run "createSecurityGroup_sharedFixture_securityGroupCreated" {
#
# Every rule chain attaches to the shared security group created by the setup
# run. Each chain uses its own port range so parallel chains cannot collide on
# a duplicate rule.
#
# Chain lifecycle (state_key = "lifecycle"; description is the only attribute
# the backend can change in place):
#   run "createSecurityGroupRule_allAttributesConfigured_completeStateReturned" {
#   run "changeDescription_existingRule_updatedInPlace" {
#   run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
#   run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
#
# Chain replacement (state_key = "replacement"; one run per attribute that
# carries a replace plan modifier):
#   run "createSecurityGroupRule_replacementChainFixture_ruleCreated" {
#   run "changePortRange_configuredPortRange_ruleReplaced" {
#   run "changeRemoteIpPrefix_configuredRemoteIpPrefix_ruleReplaced" {
#   run "changeDirection_existingRule_ruleReplaced" {
#   run "changeProtocol_existingRule_ruleReplaced" {
#
# Chain omitted_optionals (state_key = "omitted_optionals"; records what the
# backend assigns when the optional and computed values are left out):
#   run "createSecurityGroupRule_portRangeAndRemoteIpPrefixOmitted_backendValuesInState" {
#   run "planAgain_omittedOptionalValues_stateStable" {
#
# Chain normalization (state_key = "normalization"; keeps one untrimmed
# direction and protocol for the whole life of its own rule):
#   run "createSecurityGroupRule_untrimmedDirectionAndProtocol_configuredValuesPreserved" {
#   run "planAgain_untrimmedDirectionAndProtocol_stateStable" {
#
# Chain import (state_key = "import" -> "import_target"; plan-only import of the
# rule this chain creates for itself):
#   run "createSecurityGroupRule_importChainFixture_ruleCreated" {
#   run "planImport_existingSecurityGroupRuleId_completeStateRestored" {
#
# security_group.id replacement is N/A: the suite creates one shared security
# group, and moving a rule between groups needs a second one.

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


# Shared setup.

# Creates the security group before the rule chains. Its state key is destroyed
# last, after every chain releases its rule.
run "createSecurityGroup_sharedFixture_securityGroupCreated" {
  command   = apply
  state_key = "security_group"

  module {
    source = "./fixtures/security_group_rule/security_group"
  }

  variables {
    name   = "tf-test-sg-rule-shared"
    region = "vn-central-1"
  }

  assert {
    condition = (
      length(viettelcloud_security_group.shared.id) > 0 &&
      viettelcloud_security_group.shared.name == "tf-test-sg-rule-shared"
    )
    error_message = "The shared security group was not created with the expected values."
  }
}


# Chain lifecycle.

# Creates a rule with every configurable attribute set and verifies the fields
# the backend returns. ethertype is computed because the create request carries
# no address family, so the assertion checks only that the backend supplied one.
run "createSecurityGroupRule_allAttributesConfigured_completeStateReturned" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    description       = "Rule before update"
    port_range_min    = 1001
    port_range_max    = 1001
    remote_ip_prefix  = "10.10.0.0/16"
  }

  assert {
    condition = (
      length(viettelcloud_security_group_rule.rule.id) > 0 &&
      viettelcloud_security_group_rule.rule.security_group.id == run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id &&
      viettelcloud_security_group_rule.rule.security_group_name == run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_name &&
      viettelcloud_security_group_rule.rule.direction == "ingress" &&
      viettelcloud_security_group_rule.rule.protocol == "tcp" &&
      length(viettelcloud_security_group_rule.rule.ethertype) > 0 &&
      viettelcloud_security_group_rule.rule.description == "Rule before update" &&
      viettelcloud_security_group_rule.rule.port_range_min == 1001 &&
      viettelcloud_security_group_rule.rule.port_range_max == 1001 &&
      viettelcloud_security_group_rule.rule.remote_ip_prefix == "10.10.0.0/16" &&
      length(viettelcloud_security_group_rule.rule.region) > 0 &&
      length(viettelcloud_security_group_rule.rule.created_at) > 0 &&
      length(viettelcloud_security_group_rule.rule.updated_at) > 0
    )
    error_message = "The security group rule was not created with the expected values."
  }
}

# Changing the description keeps the rule and its identity.
run "changeDescription_existingRule_updatedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    description       = "Rule after update"
    port_range_min    = 1001
    port_range_max    = 1001
    remote_ip_prefix  = "10.10.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id == run.createSecurityGroupRule_allAttributesConfigured_completeStateReturned.rule_id &&
      viettelcloud_security_group_rule.rule.description == "Rule after update"
    )
    error_message = "The security group rule was replaced or its description was not updated."
  }
}

# Omitting an already configured description preserves its backend value.
run "omitDescription_descriptionSet_descriptionPreservedInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    port_range_min    = 1001
    port_range_max    = 1001
    remote_ip_prefix  = "10.10.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id == run.createSecurityGroupRule_allAttributesConfigured_completeStateReturned.rule_id &&
      viettelcloud_security_group_rule.rule.description == "Rule after update"
    )
    error_message = "Omitting description changed the rule identity or its description."
  }
}

# An explicit empty description clears it without replacing the rule.
run "clearDescription_descriptionSet_descriptionEmptyInPlace" {
  command   = apply
  parallel  = true
  state_key = "lifecycle"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    description       = ""
    port_range_min    = 1001
    port_range_max    = 1001
    remote_ip_prefix  = "10.10.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id == run.createSecurityGroupRule_allAttributesConfigured_completeStateReturned.rule_id &&
      viettelcloud_security_group_rule.rule.description == ""
    )
    error_message = "Clearing description replaced the rule or did not persist an empty value."
  }
}


# Chain replacement.

# Creates the rule the rest of this chain replaces one attribute at a time.
run "createSecurityGroupRule_replacementChainFixture_ruleCreated" {
  command   = apply
  parallel  = true
  state_key = "replacement"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    description       = "Rule for replacement"
    port_range_min    = 2001
    port_range_max    = 2001
    remote_ip_prefix  = "10.20.0.0/16"
  }

  assert {
    condition = (
      length(viettelcloud_security_group_rule.rule.id) > 0 &&
      viettelcloud_security_group_rule.rule.port_range_min == 2001
    )
    error_message = "The replacement chain's rule was not created with the expected values."
  }
}

# A configured port range carries RequiresReplaceIfConfigured, so changing it
# creates a new rule.
run "changePortRange_configuredPortRange_ruleReplaced" {
  command   = apply
  parallel  = true
  state_key = "replacement"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    description       = "Rule for replacement"
    port_range_min    = 2002
    port_range_max    = 2002
    remote_ip_prefix  = "10.20.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id != run.createSecurityGroupRule_replacementChainFixture_ruleCreated.rule_id &&
      viettelcloud_security_group_rule.rule.port_range_min == 2002 &&
      viettelcloud_security_group_rule.rule.port_range_max == 2002
    )
    error_message = "Changing the configured port range did not replace the rule."
  }
}

# A configured remote_ip_prefix carries RequiresReplaceIfConfigured.
run "changeRemoteIpPrefix_configuredRemoteIpPrefix_ruleReplaced" {
  command   = apply
  parallel  = true
  state_key = "replacement"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    description       = "Rule for replacement"
    port_range_min    = 2002
    port_range_max    = 2002
    remote_ip_prefix  = "10.21.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id != run.changePortRange_configuredPortRange_ruleReplaced.rule_id &&
      viettelcloud_security_group_rule.rule.remote_ip_prefix == "10.21.0.0/16"
    )
    error_message = "Changing the configured remote_ip_prefix did not replace the rule."
  }
}

# direction is immutable and carries RequiresReplace.
run "changeDirection_existingRule_ruleReplaced" {
  command   = apply
  parallel  = true
  state_key = "replacement"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "egress"
    protocol          = "tcp"
    description       = "Rule for replacement"
    port_range_min    = 2002
    port_range_max    = 2002
    remote_ip_prefix  = "10.21.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id != run.changeRemoteIpPrefix_configuredRemoteIpPrefix_ruleReplaced.rule_id &&
      viettelcloud_security_group_rule.rule.direction == "egress"
    )
    error_message = "Changing direction did not replace the rule."
  }
}

# protocol is immutable and carries RequiresReplace. This run ends the chain and
# its rule is destroyed at teardown.
run "changeProtocol_existingRule_ruleReplaced" {
  command   = apply
  parallel  = true
  state_key = "replacement"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "egress"
    protocol          = "udp"
    description       = "Rule for replacement"
    port_range_min    = 2002
    port_range_max    = 2002
    remote_ip_prefix  = "10.21.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id != run.changeDirection_existingRule_ruleReplaced.rule_id &&
      viettelcloud_security_group_rule.rule.protocol == "udp"
    )
    error_message = "Changing protocol did not replace the rule."
  }
}


# Chain omitted_optionals.

# port_range_min, port_range_max, and remote_ip_prefix are optional and
# computed, so whatever the backend assigns when they are omitted must land in
# state rather than staying unknown. This run records that value instead of
# asserting one, because the assignment is the backend's to make.
run "createSecurityGroupRule_portRangeAndRemoteIpPrefixOmitted_backendValuesInState" {
  command   = apply
  parallel  = true
  state_key = "omitted_optionals"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "icmp"
    description       = "Rule with omitted optional values"
  }

  assert {
    condition = (
      length(viettelcloud_security_group_rule.rule.id) > 0 &&
      length(viettelcloud_security_group_rule.rule.ethertype) > 0 &&
      viettelcloud_security_group_rule.rule.description == "Rule with omitted optional values"
    )
    error_message = "The rule with omitted optional values was not created."
  }
}

# Replanning must not turn the backend-assigned values into a diff.
run "planAgain_omittedOptionalValues_stateStable" {
  command   = plan
  parallel  = true
  state_key = "omitted_optionals"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "icmp"
    description       = "Rule with omitted optional values"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id == run.createSecurityGroupRule_portRangeAndRemoteIpPrefixOmitted_backendValuesInState.rule_id &&
      viettelcloud_security_group_rule.rule.port_range_min == run.createSecurityGroupRule_portRangeAndRemoteIpPrefixOmitted_backendValuesInState.rule_port_range_min &&
      viettelcloud_security_group_rule.rule.port_range_max == run.createSecurityGroupRule_portRangeAndRemoteIpPrefixOmitted_backendValuesInState.rule_port_range_max &&
      viettelcloud_security_group_rule.rule.remote_ip_prefix == run.createSecurityGroupRule_portRangeAndRemoteIpPrefixOmitted_backendValuesInState.rule_remote_ip_prefix
    )
    error_message = "Omitted optional values drifted or planned a replacement."
  }
}


# Chain normalization.

# The provider trims and lowercases direction and protocol before the request
# and keeps the configured representation in state, so an untrimmed value is not
# drift. Both attributes carry RequiresReplace, so this chain keeps the same
# padded strings for the whole life of its rule.
run "createSecurityGroupRule_untrimmedDirectionAndProtocol_configuredValuesPreserved" {
  command   = apply
  parallel  = true
  state_key = "normalization"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "  INGRESS  "
    protocol          = "  TCP  "
    description       = "Rule with untrimmed enums"
    port_range_min    = 3001
    port_range_max    = 3001
    remote_ip_prefix  = "10.30.0.0/16"
  }

  assert {
    condition = (
      length(viettelcloud_security_group_rule.rule.id) > 0 &&
      viettelcloud_security_group_rule.rule.direction == "  INGRESS  " &&
      viettelcloud_security_group_rule.rule.protocol == "  TCP  "
    )
    error_message = "The configured direction and protocol representations were not preserved on create."
  }
}

# Replanning the untrimmed values must remain stable and must not plan a
# replacement.
run "planAgain_untrimmedDirectionAndProtocol_stateStable" {
  command   = plan
  parallel  = true
  state_key = "normalization"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "  INGRESS  "
    protocol          = "  TCP  "
    description       = "Rule with untrimmed enums"
    port_range_min    = 3001
    port_range_max    = 3001
    remote_ip_prefix  = "10.30.0.0/16"
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.rule.id == run.createSecurityGroupRule_untrimmedDirectionAndProtocol_configuredValuesPreserved.rule_id &&
      viettelcloud_security_group_rule.rule.direction == "  INGRESS  " &&
      viettelcloud_security_group_rule.rule.protocol == "  TCP  "
    )
    error_message = "The untrimmed direction or protocol caused drift or planned a replacement."
  }
}


# Chain import.

# The import chain creates the rule it later imports so no other chain has to
# hand it over. description, the port range, and remote_ip_prefix are
# deliberately set: the import fixture leaves them unconfigured, so the values
# the plan reports can only come from the import refresh.
run "createSecurityGroupRule_importChainFixture_ruleCreated" {
  command   = apply
  parallel  = true
  state_key = "import"

  module {
    source = "./fixtures/security_group_rule/resource"
  }

  variables {
    security_group_id = run.createSecurityGroup_sharedFixture_securityGroupCreated.security_group_id
    direction         = "ingress"
    protocol          = "tcp"
    description       = "Rule for import"
    port_range_min    = 4001
    port_range_max    = 4001
    remote_ip_prefix  = "10.40.0.0/16"
  }

  assert {
    condition = (
      length(viettelcloud_security_group_rule.rule.id) > 0 &&
      viettelcloud_security_group_rule.rule.description == "Rule for import"
    )
    error_message = "The import chain's rule was not created with the expected values."
  }
}

# Importing the rule restores its state from the backend and plans no change.
# The run stays plan-only and uses its own state key. Otherwise the rule that
# the previous run owns would gain a second owner, and teardown would delete it
# twice. The import block itself lives in the fixture module, because that
# module is this run's root module.
#
# Every input comes from the creating run's observed state, so nothing below is
# a literal that could drift away from the object under test. The assertions
# check the attributes that the import refresh alone supplies: description, the
# port range, and remote_ip_prefix are unconfigured in the fixture, and the rest
# are computed. security_group, direction, and protocol are configured, so
# their planned values come from configuration and cannot prove a refresh. What
# keeps them honest is that any planned change at all makes every computed
# attribute unknown and fails this run. Their per-attribute mapping is proven
# offline by TestPopulateSecurityGroupRuleResourceStateFillsNullStateAfterImport.
run "planImport_existingSecurityGroupRuleId_completeStateRestored" {
  command   = plan
  parallel  = true
  state_key = "import_target"

  module {
    source = "./fixtures/security_group_rule/import"
  }

  variables {
    security_group_rule_id = run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_id
    security_group_id      = run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_security_group_id
    direction              = run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_direction
    protocol               = run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_protocol
  }

  assert {
    condition = (
      viettelcloud_security_group_rule.imported.id == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_id &&
      viettelcloud_security_group_rule.imported.security_group_name == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_security_group_name &&
      viettelcloud_security_group_rule.imported.ethertype == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_ethertype &&
      viettelcloud_security_group_rule.imported.description == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_description &&
      viettelcloud_security_group_rule.imported.port_range_min == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_port_range_min &&
      viettelcloud_security_group_rule.imported.port_range_max == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_port_range_max &&
      viettelcloud_security_group_rule.imported.remote_ip_prefix == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_remote_ip_prefix &&
      viettelcloud_security_group_rule.imported.region == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_region &&
      viettelcloud_security_group_rule.imported.created_at == run.createSecurityGroupRule_importChainFixture_ruleCreated.rule_created_at &&
      length(viettelcloud_security_group_rule.imported.updated_at) > 0
    )
    error_message = "Importing the security group rule did not restore the state the backend reports, or it planned a change."
  }
}
