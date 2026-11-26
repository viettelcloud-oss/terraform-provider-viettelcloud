# Test index. Copy a run declaration below to find its block.
# The chains own separate server groups under existing load balancers.
# Runs stay serial because mutations of one parent can return busy conflicts.
# Run this file separately from the data-source suite for the same reason.
# Terraform teardown removes every group and retains both existing parents.
# Duplicate-name error classification remains an offline unit-test scenario.
# A real duplicate request needs separate API evidence because native test
# expect_failures does not accept provider diagnostics.
#
# Not automated:
# - A group deleted outside Terraform leaves state on refresh (AC7). A run
#   cannot delete a group that another state owns; unit tests cover it.
#
# Chain lifecycle (state_key = "lifecycle"):
#   run "createServerGroup_allAttributesConfigured_completeStateReturned" {
#   run "planAgain_configuredServerGroup_stateStable" {
#   run "renameServerGroup_computedInputsRemoved_valuesPreserved" {
#   run "planAgain_computedInputsRemoved_stateStable" {
#   run "disablePersistence_explicitNone_valuesClearedInPlace" {
#   run "updateServerGroup_paddedNestedInputs_configuredValuesPreserved" {
#   run "planAgain_paddedNestedInputs_stateStable" {
#   run "changeMonitor_httpToTcp_httpFieldsAbsent" {
#   run "removeMonitor_computedInputsOmitted_otherValuesPreserved" {
#   run "planAgain_monitorRemoved_stateStable" {
# Chain omitted (state_key = "omitted"):
#   run "createServerGroup_optionalInputsOmitted_backendValuesReturned" {
#   run "planAgain_optionalInputsOmitted_stateStable" {
# Chain https (state_key = "https"):
#   run "createServerGroup_httpsH2Omitted_h2EnabledByBackend" {
#   run "planAgain_httpsH2Omitted_stateStable" {
#   run "planAgain_httpsH2True_stateStable" {
# Chain replacement (state_key = "replacement"):
#   run "createServerGroup_replacementChain_groupCreated" {
#   run "changeProtocol_httpToTcp_groupReplaced" {
#   run "changeLoadBalancer_secondParent_groupReplaced" {
# Chain import (state_key = "import"):
#   run "createServerGroup_importChain_groupCreated" {
# Chain import_target (state_key = "import_target"):
#   run "planImport_existingServerGroup_refreshRestoresComputedValues" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

variable "server_group_load_balancer_id" {
  description = "Existing load balancer UUID for server group tests."
  type        = string
}

variable "server_group_second_load_balancer_id" {
  description = "A distinct existing load balancer UUID for replacement tests."
  type        = string
  validation {
    condition     = var.server_group_second_load_balancer_id != var.server_group_load_balancer_id
    error_message = "The replacement test requires two different load balancers."
  }
}

run "createServerGroup_allAttributesConfigured_completeStateReturned" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = "tf-int-server-group-lifecycle"
    description         = "Initial description"
    enable_h2           = true
    session_persistence = { type = "APP_COOKIE", cookie_name = "SESSIONID" }
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = "/health"
      http_method      = "GET"
      expected_codes   = "200"
    }
  }
  assert {
    condition = (
      length(output.subject.id) > 0 &&
      output.subject.load_balancer.id == var.server_group_load_balancer_id &&
      output.subject.name == "tf-int-server-group-lifecycle" &&
      output.subject.protocol == "HTTP" &&
      output.subject.algorithm == "ROUND_ROBIN" &&
      output.subject.description == "Initial description" &&
      output.subject.enable_h2 == true &&
      output.subject.session_persistence.type == "APP_COOKIE" &&
      output.subject.session_persistence.cookie_name == "SESSIONID" &&
      output.subject.health_check.monitor_type == "HTTP" &&
      output.subject.health_check.delay == 10 &&
      output.subject.health_check.timeout == 5 &&
      output.subject.health_check.max_retries == 3 &&
      output.subject.health_check.max_retries_down == 3 &&
      output.subject.health_check.path == "/health" &&
      output.subject.health_check.http_method == "GET" &&
      output.subject.health_check.expected_codes == "200" &&
      output.subject.member_count == 0 &&
      output.subject.provisioning_status == "ACTIVE" &&
      length(output.subject.status) > 0 &&
      length(output.subject.created_at) > 0 &&
      length(output.subject.updated_at) > 0
    )
    error_message = "Server group createServerGroup_allAttributesConfigured_completeStateReturned returned unexpected state."
  }
}

run "planAgain_configuredServerGroup_stateStable" {
  command   = plan
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = "tf-int-server-group-lifecycle"
    description         = "Initial description"
    enable_h2           = true
    session_persistence = { type = "APP_COOKIE", cookie_name = "SESSIONID" }
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = "/health"
      http_method      = "GET"
      expected_codes   = "200"
    }
  }
  assert {
    condition = (
      output.subject == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject
    )
    error_message = "Server group planAgain_configuredServerGroup_stateStable returned unexpected state."
  }
}

# Removal of optional computed inputs preserves the values from the prior run.
run "renameServerGroup_computedInputsRemoved_valuesPreserved" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = "tf-int-server-group-renamed"
    description         = null
    enable_h2           = null
    session_persistence = null
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = "/health"
      http_method      = "GET"
      expected_codes   = "200"
    }
  }
  assert {
    condition = (
      output.subject.id == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.id &&
      output.subject.name == "tf-int-server-group-renamed" &&
      output.subject.description == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.description &&
      output.subject.enable_h2 == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.enable_h2 &&
      output.subject.session_persistence == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.session_persistence
    )
    error_message = "Server group renameServerGroup_computedInputsRemoved_valuesPreserved returned unexpected state."
  }
}

run "planAgain_computedInputsRemoved_stateStable" {
  command   = plan
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = "tf-int-server-group-renamed"
    description         = null
    enable_h2           = null
    session_persistence = null
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = "/health"
      http_method      = "GET"
      expected_codes   = "200"
    }
  }
  assert {
    condition = (
      output.subject == run.renameServerGroup_computedInputsRemoved_valuesPreserved.subject
    )
    error_message = "Server group planAgain_computedInputsRemoved_stateStable returned unexpected state."
  }
}

# This run tests whether the backend returns NONE as a known type rather than null.
run "disablePersistence_explicitNone_valuesClearedInPlace" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = "tf-int-server-group-renamed"
    description         = ""
    enable_h2           = false
    session_persistence = { type = "NONE" }
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = "/health"
      http_method      = "GET"
      expected_codes   = "200"
    }
  }
  assert {
    condition = (
      output.subject.id == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.id &&
      output.subject.description == "" &&
      output.subject.enable_h2 == false &&
      output.subject.session_persistence.type == "NONE" &&
      output.subject.session_persistence.cookie_name == null
    )
    error_message = "Server group disablePersistence_explicitNone_valuesClearedInPlace returned unexpected state."
  }
}

run "updateServerGroup_paddedNestedInputs_configuredValuesPreserved" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = " tf-int-server-group-renamed "
    description         = ""
    enable_h2           = false
    session_persistence = { type = " APP_COOKIE ", cookie_name = " SESSIONID " }
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = " /health "
      http_method      = " GET "
      expected_codes   = " 200 "
    }
    algorithm = "LEAST_CONNECTIONS"
  }
  assert {
    condition = (
      output.subject.id == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.id &&
      output.subject.name == " tf-int-server-group-renamed " &&
      output.subject.algorithm == "LEAST_CONNECTIONS" &&
      output.subject.session_persistence.type == " APP_COOKIE " &&
      output.subject.session_persistence.cookie_name == " SESSIONID " &&
      output.subject.health_check.path == " /health " &&
      output.subject.health_check.http_method == " GET " &&
      output.subject.health_check.expected_codes == " 200 "
    )
    error_message = "Server group updateServerGroup_paddedNestedInputs_configuredValuesPreserved returned unexpected state."
  }
}

run "planAgain_paddedNestedInputs_stateStable" {
  command   = plan
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = " tf-int-server-group-renamed "
    description         = ""
    enable_h2           = false
    session_persistence = { type = " APP_COOKIE ", cookie_name = " SESSIONID " }
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = " /health "
      http_method      = " GET "
      expected_codes   = " 200 "
    }
    algorithm = "LEAST_CONNECTIONS"
  }
  assert {
    condition = (
      output.subject == run.updateServerGroup_paddedNestedInputs_configuredValuesPreserved.subject
    )
    error_message = "Server group planAgain_paddedNestedInputs_stateStable returned unexpected state."
  }
}

# This run tests whether TCP monitor responses omit the HTTP fields.
run "changeMonitor_httpToTcp_httpFieldsAbsent" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = " tf-int-server-group-renamed "
    description         = ""
    enable_h2           = false
    session_persistence = { type = " APP_COOKIE ", cookie_name = " SESSIONID " }
    health_check = {
      monitor_type     = "TCP"
      delay            = 15
      timeout          = 5
      max_retries      = 2
      max_retries_down = 2
    }
    algorithm = "LEAST_CONNECTIONS"
  }
  assert {
    condition = (
      output.subject.id == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.id &&
      output.subject.health_check.monitor_type == "TCP" &&
      output.subject.health_check.delay == 15 &&
      output.subject.health_check.max_retries == 2 &&
      output.subject.health_check.path == null &&
      output.subject.health_check.http_method == null &&
      output.subject.health_check.expected_codes == null
    )
    error_message = "Server group changeMonitor_httpToTcp_httpFieldsAbsent returned unexpected state."
  }
}

# A full update removes the monitor and carries forward unconfigured computed values.
run "removeMonitor_computedInputsOmitted_otherValuesPreserved" {
  command   = apply
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = " tf-int-server-group-renamed "
    description         = null
    enable_h2           = null
    session_persistence = null
    health_check        = null
    algorithm           = "LEAST_CONNECTIONS"
  }
  assert {
    condition = (
      output.subject.id == run.createServerGroup_allAttributesConfigured_completeStateReturned.subject.id &&
      output.subject.health_check == null &&
      output.subject.description == run.changeMonitor_httpToTcp_httpFieldsAbsent.subject.description &&
      output.subject.enable_h2 == run.changeMonitor_httpToTcp_httpFieldsAbsent.subject.enable_h2 &&
      output.subject.session_persistence == run.changeMonitor_httpToTcp_httpFieldsAbsent.subject.session_persistence
    )
    error_message = "Server group removeMonitor_computedInputsOmitted_otherValuesPreserved returned unexpected state."
  }
}

run "planAgain_monitorRemoved_stateStable" {
  command   = plan
  parallel  = false
  state_key = "lifecycle"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = " tf-int-server-group-renamed "
    description         = null
    enable_h2           = null
    session_persistence = null
    health_check        = null
    algorithm           = "LEAST_CONNECTIONS"
  }
  assert {
    condition = (
      output.subject == run.removeMonitor_computedInputsOmitted_otherValuesPreserved.subject
    )
    error_message = "Server group planAgain_monitorRemoved_stateStable returned unexpected state."
  }
}

# Creation omits description and enable_h2 and requests NONE persistence.
# The Swagger ServerGroupCreateSchema defaults description to "", and the
# response carries it. For an HTTP group the backend returns enable_h2 as null
# when the request omits it, so the state holds null and not the Swagger
# default false.
# session_persistence must be a non-null NONE, because a null value blocks
# every later full PUT of this group.
run "createServerGroup_optionalInputsOmitted_backendValuesReturned" {
  command   = apply
  parallel  = false
  state_key = "omitted"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_load_balancer_id
    name             = "tf-int-server-group-omitted"
  }
  assert {
    condition = (
      length(output.subject.id) > 0 &&
      output.subject.description == "" &&
      output.subject.enable_h2 == null &&
      output.subject.health_check == null &&
      output.subject.session_persistence.type == "NONE"
    )
    error_message = "Server group createServerGroup_optionalInputsOmitted_backendValuesReturned returned unexpected state."
  }
}

run "planAgain_optionalInputsOmitted_stateStable" {
  command   = plan
  parallel  = false
  state_key = "omitted"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_load_balancer_id
    name             = "tf-int-server-group-omitted"
  }
  assert {
    condition = (
      output.subject == run.createServerGroup_optionalInputsOmitted_backendValuesReturned.subject
    )
    error_message = "Server group planAgain_optionalInputsOmitted_stateStable returned unexpected state."
  }
}

# An HTTPS group always uses HTTP/2: the API stores enable_h2 = true even
# when the request omits it. The provider rejects enable_h2 = false for HTTPS
# at plan time, so this chain covers only omission and an explicit true.
run "createServerGroup_httpsH2Omitted_h2EnabledByBackend" {
  command   = apply
  parallel  = false
  state_key = "https"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_load_balancer_id
    name             = "tf-int-server-group-https"
    protocol         = "HTTPS"
  }
  assert {
    condition = (
      length(output.subject.id) > 0 &&
      output.subject.protocol == "HTTPS" &&
      output.subject.enable_h2 == true &&
      output.subject.provisioning_status == "ACTIVE"
    )
    error_message = "Server group createServerGroup_httpsH2Omitted_h2EnabledByBackend returned unexpected state."
  }
}

run "planAgain_httpsH2Omitted_stateStable" {
  command   = plan
  parallel  = false
  state_key = "https"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_load_balancer_id
    name             = "tf-int-server-group-https"
    protocol         = "HTTPS"
  }
  assert {
    condition = (
      output.subject == run.createServerGroup_httpsH2Omitted_h2EnabledByBackend.subject
    )
    error_message = "Server group planAgain_httpsH2Omitted_stateStable returned unexpected state."
  }
}

# A configured true matches the value that the API stored, so the plan keeps
# the group unchanged.
run "planAgain_httpsH2True_stateStable" {
  command   = plan
  parallel  = false
  state_key = "https"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_load_balancer_id
    name             = "tf-int-server-group-https"
    protocol         = "HTTPS"
    enable_h2        = true
  }
  assert {
    condition = (
      output.subject == run.createServerGroup_httpsH2Omitted_h2EnabledByBackend.subject
    )
    error_message = "Server group planAgain_httpsH2True_stateStable returned unexpected state."
  }
}

run "createServerGroup_replacementChain_groupCreated" {
  command   = apply
  parallel  = false
  state_key = "replacement"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_load_balancer_id
    name             = "tf-int-server-group-replacement"
    protocol         = "HTTP"
  }
  assert {
    condition = (
      length(output.subject.id) > 0
    )
    error_message = "Server group createServerGroup_replacementChain_groupCreated returned unexpected state."
  }
}

run "changeProtocol_httpToTcp_groupReplaced" {
  command   = apply
  parallel  = false
  state_key = "replacement"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_load_balancer_id
    name             = "tf-int-server-group-replacement"
    protocol         = "TCP"
  }
  assert {
    condition = (
      output.subject.id != run.createServerGroup_replacementChain_groupCreated.subject.id &&
      output.subject.protocol == "TCP"
    )
    error_message = "Server group changeProtocol_httpToTcp_groupReplaced returned unexpected state."
  }
}

run "changeLoadBalancer_secondParent_groupReplaced" {
  command   = apply
  parallel  = false
  state_key = "replacement"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id = var.server_group_second_load_balancer_id
    name             = "tf-int-server-group-replacement"
    protocol         = "TCP"
  }
  assert {
    condition = (
      output.subject.id != run.changeProtocol_httpToTcp_groupReplaced.subject.id &&
      output.subject.load_balancer.id == var.server_group_second_load_balancer_id
    )
    error_message = "Server group changeLoadBalancer_secondParent_groupReplaced returned unexpected state."
  }
}

run "createServerGroup_importChain_groupCreated" {
  command   = apply
  parallel  = false
  state_key = "import"
  module { source = "./fixtures/lb_server_group/resource" }
  variables {
    load_balancer_id    = var.server_group_load_balancer_id
    name                = "tf-int-server-group-import"
    description         = "Initial description"
    enable_h2           = true
    session_persistence = { type = "APP_COOKIE", cookie_name = "SESSIONID" }
    health_check = {
      monitor_type     = "HTTP"
      delay            = 10
      timeout          = 5
      max_retries      = 3
      max_retries_down = 3
      path             = "/health"
      http_method      = "GET"
      expected_codes   = "200"
    }
  }
  assert {
    condition = (
      length(output.subject.id) > 0
    )
    error_message = "Server group createServerGroup_importChain_groupCreated returned unexpected state."
  }
}

# Import leaves computed inputs unconfigured. Its plan must contain no changes.
run "planImport_existingServerGroup_refreshRestoresComputedValues" {
  command   = plan
  parallel  = false
  state_key = "import_target"
  module { source = "./fixtures/lb_server_group/import" }
  variables {
    server_group_id  = run.createServerGroup_importChain_groupCreated.subject.id
    load_balancer_id = run.createServerGroup_importChain_groupCreated.subject.load_balancer.id
    name             = run.createServerGroup_importChain_groupCreated.subject.name
    protocol         = run.createServerGroup_importChain_groupCreated.subject.protocol
    algorithm        = run.createServerGroup_importChain_groupCreated.subject.algorithm
    health_check     = run.createServerGroup_importChain_groupCreated.subject.health_check
  }
  assert {
    condition = (
      output.subject.id == run.createServerGroup_importChain_groupCreated.subject.id &&
      output.subject.description == run.createServerGroup_importChain_groupCreated.subject.description &&
      output.subject.enable_h2 == run.createServerGroup_importChain_groupCreated.subject.enable_h2 &&
      output.subject.session_persistence == run.createServerGroup_importChain_groupCreated.subject.session_persistence &&
      output.subject.created_at == run.createServerGroup_importChain_groupCreated.subject.created_at &&
      output.subject.updated_at == run.createServerGroup_importChain_groupCreated.subject.updated_at &&
      output.subject.status == run.createServerGroup_importChain_groupCreated.subject.status &&
      output.subject.member_count == run.createServerGroup_importChain_groupCreated.subject.member_count &&
      output.subject.provisioning_status == run.createServerGroup_importChain_groupCreated.subject.provisioning_status
    )
    error_message = "Server group planImport_existingServerGroup_refreshRestoresComputedValues returned unexpected state."
  }
}
