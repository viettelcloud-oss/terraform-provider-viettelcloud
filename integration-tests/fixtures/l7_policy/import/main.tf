# Import fixture for tests/l7_policy_resource.tftest.hcl. It imports the
# policy that run "createPolicy_importChainFixture_policyCreated" or run
# "createPolicy_externalDeletionChainFixture_policyCreated" created, rather
# than creating one of its own.
#
# Run "planImport_existingPolicy_refreshRestoresComputedValues" only plans
# it, because state_key "import" owns its policy and a second owner would
# delete that policy twice at teardown. Run
# "importPolicy_secondOwner_policyAdopted" applies it under state_key
# "external_owner", which then deletes the policy outside state_key
# "external".

terraform {
  required_version = ">= 1.16"

  required_providers {
    viettelcloud = {
      source = "viettelcloud-oss/viettelcloud"
    }
  }
}

variable "l7_policy_id" {
  type = string
}

variable "listener_id" {
  type = string
}

variable "name" {
  type = string
}

variable "action" {
  type = string
}

variable "redirect_url" {
  type = string
}

# The run loads this fixture through its module block. Keep the import here
# because an import block inside a run block is unsupported.
import {
  to = viettelcloud_lb_l7_policy.imported
  id = var.l7_policy_id
}

# The required attributes and the target of the URL action are fed from the
# state of run "createPolicy_importChainFixture_policyCreated" or run
# "createPolicy_externalDeletionChainFixture_policyCreated", whichever
# created this policy. description, position, and
# redirect_http_code stay unset, because omitting them keeps the imported
# values.
resource "viettelcloud_lb_l7_policy" "imported" {
  listener     = { id = var.listener_id }
  name         = var.name
  action       = var.action
  redirect_url = var.redirect_url
}
