# Chains run one after another because the backend locks the caller-owned
# load balancer during each listener mutation and refuses a concurrent one as
# busy.
# Each chain uses its own listeners and ports on that load balancer.
# Port offset 4 belongs to lb_listener_data_source.tftest.hcl, which uses
# the same load balancer and base port.
# The import_target state only plans the object owned by state import.
# Omission retains SNI certificates and Boolean values. Removing a server
# group or default TLS certificate detaches it. No run detaches the default
# TLS certificate: the backend rejects that on TERMINATED_HTTPS with HTTP 400,
# and no listener of another protocol in this file has a certificate.
# Each planAgain run compares updated_at with the apply run before it.
# A planned change leaves updated_at unknown and fails the run.
# No run creates a TERMINATED_HTTPS listener without SNI certificates or
# clears them. The backend rejects both with HTTP 400 invalid_input.
# HTTPS and TERMINATED_HTTPS always report enable_h2 = true, and the provider
# rejects false for them at plan time. So the HTTP lifecycle chain covers the
# in-place enable_h2 update and enable_h2 = false.
#
# Chain lifecycle (state_key = "lifecycle").
#   run "createListener_requiredAttributesOnly_completeStateReturned" {
#   run "changeMutableAttributes_existingListener_updatedInPlace" {
#   run "planAgain_untrimmedNameAndConfiguredValues_stateStable" {
#   run "clearDescriptionCidrsForwardingAndH2_existingListener_updatedInPlace" {
#   run "omitDescriptionCidrsAndTimeouts_existingListener_supportedOmissionApplied" {
#   run "planAgain_omittedAttributes_stateStable" {
#   run "changePort_existingListener_listenerReplaced" {
#   run "changeProtocol_existingHttpListener_listenerReplaced" {
#
# Chain tls (state_key = "tls").
#   run "createListener_tlsAndServerGroupConfigured_completeStateReturned" {
#   run "changeName_existingTlsListener_updatedInPlace" {
#   run "omitSniCertificatesAndH2_existingTlsListener_valuesPreserved" {
#   run "planAgain_tlsValuesOmitted_stateStable" {
#   run "detachServerGroup_existingTlsListener_serverGroupRemoved" {
#   run "planAgain_serverGroupDetached_stateStable" {
#
# Chain tls_default (state_key = "tls_default").
#   run "createListener_tlsH2Omitted_h2EnabledByBackend" {
#   run "planAgain_tlsH2Omitted_stateStable" {
#
# Chain import (state_key = "import").
#   run "createListener_importChainFixture_listenerCreated" {
#
# Chain import_target (state_key = "import_target").
#   run "planImport_existingCompositeId_backendStateRestored" {

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
variable "listener_load_balancer_id" {
  type = string
}
variable "listener_server_group_id" {
  type = string
}
variable "listener_tls_certificate_id" {
  type = string
}
variable "listener_port" {
  type    = number
  default = 18080
}

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

run "createListener_requiredAttributesOnly_completeStateReturned" {
  command   = apply
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id = var.listener_load_balancer_id
    name             = "tf-test-listener-lifecycle"
    protocol         = "HTTP"
    protocol_port    = var.listener_port
  }
  assert {
    condition = (length(viettelcloud_lb_listener.listener.id) > 0 && viettelcloud_lb_listener.listener.name == "tf-test-listener-lifecycle" &&
      viettelcloud_lb_listener.listener.load_balancer.id == var.listener_load_balancer_id && viettelcloud_lb_listener.listener.protocol == "HTTP" &&
      viettelcloud_lb_listener.listener.protocol_port == var.listener_port && viettelcloud_lb_listener.listener.provisioning_status == "ACTIVE" &&
      viettelcloud_lb_listener.listener.status != null && viettelcloud_lb_listener.listener.listener_timeout == 50000 && viettelcloud_lb_listener.listener.server_timeout == 50000 &&
      viettelcloud_lb_listener.listener.server_connecting_timeout == 5000 && length(viettelcloud_lb_listener.listener.created_at) > 0 && length(viettelcloud_lb_listener.listener.updated_at) > 0 &&
      viettelcloud_lb_listener.listener.description == null && viettelcloud_lb_listener.listener.server_group == null && viettelcloud_lb_listener.listener.default_tls_cert == null &&
    viettelcloud_lb_listener.listener.sni_tls_certs == null && viettelcloud_lb_listener.listener.allowed_cidrs == null)
    error_message = "Listener createListener_requiredAttributesOnly_completeStateReturned did not return the expected state."
  }
}

run "changeMutableAttributes_existingListener_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id          = var.listener_load_balancer_id
    name                      = "  tf-test-listener-updated  "
    protocol                  = "HTTP"
    protocol_port             = var.listener_port
    description               = "Updated listener"
    allowed_cidrs             = ["192.0.2.0/24", "198.51.100.0/24"]
    listener_timeout          = 60000
    server_timeout            = 70000
    server_connecting_timeout = 10000
    enable_h2                 = true
    set_x_forwarded_for       = true
    set_x_forwarded_port      = true
    set_x_forwarded_proto     = true
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_requiredAttributesOnly_completeStateReturned.listener.id && viettelcloud_lb_listener.listener.name == "  tf-test-listener-updated  " &&
      viettelcloud_lb_listener.listener.description == "Updated listener" && viettelcloud_lb_listener.listener.allowed_cidrs == toset(["192.0.2.0/24", "198.51.100.0/24"]) &&
      viettelcloud_lb_listener.listener.listener_timeout == 60000 && viettelcloud_lb_listener.listener.server_timeout == 70000 && viettelcloud_lb_listener.listener.server_connecting_timeout == 10000 &&
    viettelcloud_lb_listener.listener.enable_h2 == true && viettelcloud_lb_listener.listener.set_x_forwarded_for == true && viettelcloud_lb_listener.listener.set_x_forwarded_port == true && viettelcloud_lb_listener.listener.set_x_forwarded_proto == true)
    error_message = "Listener changeMutableAttributes_existingListener_updatedInPlace did not return the expected state."
  }
}

run "planAgain_untrimmedNameAndConfiguredValues_stateStable" {
  command   = plan
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id          = var.listener_load_balancer_id
    name                      = "  tf-test-listener-updated  "
    protocol                  = "HTTP"
    protocol_port             = var.listener_port
    description               = "Updated listener"
    allowed_cidrs             = ["192.0.2.0/24", "198.51.100.0/24"]
    listener_timeout          = 60000
    server_timeout            = 70000
    server_connecting_timeout = 10000
    enable_h2                 = true
    set_x_forwarded_for       = true
    set_x_forwarded_port      = true
    set_x_forwarded_proto     = true
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_requiredAttributesOnly_completeStateReturned.listener.id &&
    viettelcloud_lb_listener.listener.updated_at == run.changeMutableAttributes_existingListener_updatedInPlace.listener.updated_at)
    error_message = "Listener planAgain_untrimmedNameAndConfiguredValues_stateStable did not return the expected state."
  }
}

run "clearDescriptionCidrsForwardingAndH2_existingListener_updatedInPlace" {
  command   = apply
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id          = var.listener_load_balancer_id
    name                      = "  tf-test-listener-updated  "
    protocol                  = "HTTP"
    protocol_port             = var.listener_port
    description               = ""
    allowed_cidrs             = []
    listener_timeout          = 60000
    server_timeout            = 70000
    server_connecting_timeout = 10000
    enable_h2                 = false
    set_x_forwarded_for       = false
    set_x_forwarded_port      = false
    set_x_forwarded_proto     = false
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_requiredAttributesOnly_completeStateReturned.listener.id && viettelcloud_lb_listener.listener.description == "" && length(viettelcloud_lb_listener.listener.allowed_cidrs) == 0 &&
    viettelcloud_lb_listener.listener.enable_h2 == false && viettelcloud_lb_listener.listener.set_x_forwarded_for == false && viettelcloud_lb_listener.listener.set_x_forwarded_port == false && viettelcloud_lb_listener.listener.set_x_forwarded_proto == false)
    error_message = "Listener clearDescriptionCidrsForwardingAndH2_existingListener_updatedInPlace did not return the expected state."
  }
}

run "omitDescriptionCidrsAndTimeouts_existingListener_supportedOmissionApplied" {
  command   = apply
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id = var.listener_load_balancer_id
    name             = "  tf-test-listener-updated  "
    protocol         = "HTTP"
    protocol_port    = var.listener_port
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_requiredAttributesOnly_completeStateReturned.listener.id && viettelcloud_lb_listener.listener.description == null && viettelcloud_lb_listener.listener.allowed_cidrs == null &&
    viettelcloud_lb_listener.listener.listener_timeout == 60000 && viettelcloud_lb_listener.listener.server_timeout == 70000 && viettelcloud_lb_listener.listener.server_connecting_timeout == 10000 && viettelcloud_lb_listener.listener.enable_h2 == false && viettelcloud_lb_listener.listener.set_x_forwarded_for == false && viettelcloud_lb_listener.listener.set_x_forwarded_port == false && viettelcloud_lb_listener.listener.set_x_forwarded_proto == false)
    error_message = "Listener omitDescriptionCidrsAndTimeouts_existingListener_supportedOmissionApplied did not return the expected state."
  }
}

run "planAgain_omittedAttributes_stateStable" {
  command   = plan
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id = var.listener_load_balancer_id
    name             = "  tf-test-listener-updated  "
    protocol         = "HTTP"
    protocol_port    = var.listener_port
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_requiredAttributesOnly_completeStateReturned.listener.id &&
    viettelcloud_lb_listener.listener.updated_at == run.omitDescriptionCidrsAndTimeouts_existingListener_supportedOmissionApplied.listener.updated_at)
    error_message = "Listener planAgain_omittedAttributes_stateStable did not return the expected state."
  }
}

run "changePort_existingListener_listenerReplaced" {
  command   = apply
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id = var.listener_load_balancer_id
    name             = "  tf-test-listener-updated  "
    protocol         = "HTTP"
    protocol_port    = var.listener_port + 1
  }
  assert {
    condition     = (viettelcloud_lb_listener.listener.id != run.createListener_requiredAttributesOnly_completeStateReturned.listener.id && viettelcloud_lb_listener.listener.protocol_port == var.listener_port + 1)
    error_message = "Listener changePort_existingListener_listenerReplaced did not return the expected state."
  }
}

run "changeProtocol_existingHttpListener_listenerReplaced" {
  command   = apply
  state_key = "lifecycle"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id = var.listener_load_balancer_id
    name             = "  tf-test-listener-updated  "
    protocol         = "TCP"
    protocol_port    = var.listener_port + 1
  }
  assert {
    condition     = (viettelcloud_lb_listener.listener.id != run.changePort_existingListener_listenerReplaced.listener.id && viettelcloud_lb_listener.listener.protocol == "TCP")
    error_message = "Listener changeProtocol_existingHttpListener_listenerReplaced did not return the expected state."
  }
}

run "createListener_tlsAndServerGroupConfigured_completeStateReturned" {
  command   = apply
  state_key = "tls"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 2
    server_group_id     = var.listener_server_group_id
    default_tls_cert_id = var.listener_tls_certificate_id
    sni_tls_cert_ids    = [var.listener_tls_certificate_id]
    allowed_cidrs       = ["192.0.2.0/24"]
    enable_h2           = true
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.default_tls_cert.id == var.listener_tls_certificate_id &&
      viettelcloud_lb_listener.listener.allowed_cidrs == toset(["192.0.2.0/24"]) &&
      toset([for cert in viettelcloud_lb_listener.listener.sni_tls_certs : cert.id]) == toset([var.listener_tls_certificate_id]) &&
    viettelcloud_lb_listener.listener.server_group.id == var.listener_server_group_id && viettelcloud_lb_listener.listener.enable_h2 == true)
    error_message = "Listener createListener_tlsAndServerGroupConfigured_completeStateReturned did not return the expected state."
  }
}

run "changeName_existingTlsListener_updatedInPlace" {
  command   = apply
  state_key = "tls"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls-updated"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 2
    server_group_id     = var.listener_server_group_id
    default_tls_cert_id = var.listener_tls_certificate_id
    sni_tls_cert_ids    = [var.listener_tls_certificate_id]
    allowed_cidrs       = ["192.0.2.0/24"]
    enable_h2           = true
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_tlsAndServerGroupConfigured_completeStateReturned.listener.id &&
      viettelcloud_lb_listener.listener.name == "tf-test-listener-tls-updated" &&
      viettelcloud_lb_listener.listener.allowed_cidrs == toset(["192.0.2.0/24"]) &&
      viettelcloud_lb_listener.listener.default_tls_cert.id == var.listener_tls_certificate_id &&
      toset([for cert in viettelcloud_lb_listener.listener.sni_tls_certs : cert.id]) == toset([var.listener_tls_certificate_id]) &&
    viettelcloud_lb_listener.listener.enable_h2 == true)
    error_message = "Listener changeName_existingTlsListener_updatedInPlace did not return the expected state."
  }
}

# server_group and default_tls_cert stay configured, because removing them
# detaches them.
run "omitSniCertificatesAndH2_existingTlsListener_valuesPreserved" {
  command   = apply
  state_key = "tls"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls-updated"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 2
    server_group_id     = var.listener_server_group_id
    default_tls_cert_id = var.listener_tls_certificate_id
  }
  assert {
    condition     = (viettelcloud_lb_listener.listener.id == run.createListener_tlsAndServerGroupConfigured_completeStateReturned.listener.id && viettelcloud_lb_listener.listener.allowed_cidrs == null && viettelcloud_lb_listener.listener.default_tls_cert.id == var.listener_tls_certificate_id && toset([for cert in viettelcloud_lb_listener.listener.sni_tls_certs : cert.id]) == toset([var.listener_tls_certificate_id]) && viettelcloud_lb_listener.listener.server_group.id == var.listener_server_group_id && viettelcloud_lb_listener.listener.enable_h2 == true)
    error_message = "Listener omitSniCertificatesAndH2_existingTlsListener_valuesPreserved did not return the expected state."
  }
}

run "planAgain_tlsValuesOmitted_stateStable" {
  command   = plan
  state_key = "tls"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls-updated"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 2
    server_group_id     = var.listener_server_group_id
    default_tls_cert_id = var.listener_tls_certificate_id
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_tlsAndServerGroupConfigured_completeStateReturned.listener.id &&
    viettelcloud_lb_listener.listener.updated_at == run.omitSniCertificatesAndH2_existingTlsListener_valuesPreserved.listener.updated_at)
    error_message = "Listener planAgain_tlsValuesOmitted_stateStable did not return the expected state."
  }
}

# The PATCH sends server_group_id as JSON null, and the refresh after it must
# report no server group.
run "detachServerGroup_existingTlsListener_serverGroupRemoved" {
  command   = apply
  state_key = "tls"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls-updated"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 2
    default_tls_cert_id = var.listener_tls_certificate_id
    enable_h2           = true
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_tlsAndServerGroupConfigured_completeStateReturned.listener.id &&
      viettelcloud_lb_listener.listener.server_group == null &&
      viettelcloud_lb_listener.listener.default_tls_cert.id == var.listener_tls_certificate_id &&
      toset([for cert in viettelcloud_lb_listener.listener.sni_tls_certs : cert.id]) == toset([var.listener_tls_certificate_id]) &&
    viettelcloud_lb_listener.listener.enable_h2 == true)
    error_message = "Listener detachServerGroup_existingTlsListener_serverGroupRemoved did not return the expected state."
  }
}

run "planAgain_serverGroupDetached_stateStable" {
  command   = plan
  state_key = "tls"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls-updated"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 2
    default_tls_cert_id = var.listener_tls_certificate_id
    enable_h2           = true
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_tlsAndServerGroupConfigured_completeStateReturned.listener.id &&
      viettelcloud_lb_listener.listener.server_group == null &&
    viettelcloud_lb_listener.listener.updated_at == run.detachServerGroup_existingTlsListener_serverGroupRemoved.listener.updated_at)
    error_message = "Listener planAgain_serverGroupDetached_stateStable did not return the expected state."
  }
}

# The backend rejects a TERMINATED_HTTPS create that omits enable_h2, and
# stores true whatever the request sends, so the provider sends true.
run "createListener_tlsH2Omitted_h2EnabledByBackend" {
  command   = apply
  state_key = "tls_default"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls-default"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 5
    default_tls_cert_id = var.listener_tls_certificate_id
    sni_tls_cert_ids    = [var.listener_tls_certificate_id]
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.provisioning_status == "ACTIVE" &&
      viettelcloud_lb_listener.listener.default_tls_cert.id == var.listener_tls_certificate_id &&
      toset([for cert in viettelcloud_lb_listener.listener.sni_tls_certs : cert.id]) == toset([var.listener_tls_certificate_id]) &&
    viettelcloud_lb_listener.listener.enable_h2 == true)
    error_message = "Listener createListener_tlsH2Omitted_h2EnabledByBackend did not return the expected state."
  }
}

run "planAgain_tlsH2Omitted_stateStable" {
  command   = plan
  state_key = "tls_default"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id    = var.listener_load_balancer_id
    name                = "tf-test-listener-tls-default"
    protocol            = "TERMINATED_HTTPS"
    protocol_port       = var.listener_port + 5
    default_tls_cert_id = var.listener_tls_certificate_id
    sni_tls_cert_ids    = [var.listener_tls_certificate_id]
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_tlsH2Omitted_h2EnabledByBackend.listener.id &&
    viettelcloud_lb_listener.listener.updated_at == run.createListener_tlsH2Omitted_h2EnabledByBackend.listener.updated_at)
    error_message = "Listener planAgain_tlsH2Omitted_stateStable did not return the expected state."
  }
}

run "createListener_importChainFixture_listenerCreated" {
  command   = apply
  state_key = "import"
  module {
    source = "./fixtures/lb_listener/resource"
  }
  variables {
    load_balancer_id = var.listener_load_balancer_id
    name             = "tf-test-listener-import"
    protocol         = "HTTP"
    protocol_port    = var.listener_port + 3
  }
  assert {
    condition     = (viettelcloud_lb_listener.listener.provisioning_status == "ACTIVE")
    error_message = "Listener createListener_importChainFixture_listenerCreated did not return the expected state."
  }
}

run "planImport_existingCompositeId_backendStateRestored" {
  command   = plan
  state_key = "import_target"
  module {
    source = "./fixtures/lb_listener/import"
  }
  variables {
    protocol         = run.createListener_importChainFixture_listenerCreated.listener.protocol
    name             = run.createListener_importChainFixture_listenerCreated.listener.name
    protocol_port    = run.createListener_importChainFixture_listenerCreated.listener.protocol_port
    load_balancer_id = run.createListener_importChainFixture_listenerCreated.listener.load_balancer.id
    listener_id      = run.createListener_importChainFixture_listenerCreated.listener.id
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.id == run.createListener_importChainFixture_listenerCreated.listener.id && viettelcloud_lb_listener.listener.created_at == run.createListener_importChainFixture_listenerCreated.listener.created_at &&
      viettelcloud_lb_listener.listener.updated_at == run.createListener_importChainFixture_listenerCreated.listener.updated_at && viettelcloud_lb_listener.listener.provisioning_status == "ACTIVE" &&
      viettelcloud_lb_listener.listener.status == run.createListener_importChainFixture_listenerCreated.listener.status && viettelcloud_lb_listener.listener.listener_timeout == run.createListener_importChainFixture_listenerCreated.listener.listener_timeout &&
      viettelcloud_lb_listener.listener.server_timeout == run.createListener_importChainFixture_listenerCreated.listener.server_timeout && viettelcloud_lb_listener.listener.server_connecting_timeout == run.createListener_importChainFixture_listenerCreated.listener.server_connecting_timeout &&
    viettelcloud_lb_listener.listener.description == null)
    error_message = "Listener planImport_existingCompositeId_backendStateRestored did not return the expected state."
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.enable_h2 == run.createListener_importChainFixture_listenerCreated.listener.enable_h2 &&
      viettelcloud_lb_listener.listener.set_x_forwarded_for == run.createListener_importChainFixture_listenerCreated.listener.set_x_forwarded_for &&
      viettelcloud_lb_listener.listener.set_x_forwarded_port == run.createListener_importChainFixture_listenerCreated.listener.set_x_forwarded_port &&
    viettelcloud_lb_listener.listener.set_x_forwarded_proto == run.createListener_importChainFixture_listenerCreated.listener.set_x_forwarded_proto)
    error_message = "Listener planImport_existingCompositeId_backendStateRestored did not restore the HTTP/2 and forwarding flags."
  }
  assert {
    condition = (viettelcloud_lb_listener.listener.server_group == run.createListener_importChainFixture_listenerCreated.listener.server_group &&
      viettelcloud_lb_listener.listener.default_tls_cert == run.createListener_importChainFixture_listenerCreated.listener.default_tls_cert &&
      viettelcloud_lb_listener.listener.sni_tls_certs == run.createListener_importChainFixture_listenerCreated.listener.sni_tls_certs &&
    viettelcloud_lb_listener.listener.allowed_cidrs == run.createListener_importChainFixture_listenerCreated.listener.allowed_cidrs)
    error_message = "Listener planImport_existingCompositeId_backendStateRestored did not restore the references, certificates, and allowed CIDRs."
  }
}
