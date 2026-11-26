# Test index — copy a run declaration below and Find Next to jump to its block.
# Chains and runs are listed in file order; setup runs are included.
#
# Chain lifecycle (shared fixture state):
#   run "createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned" {
#   run "refresh_existingKeyPair_statePreserved" {
#   run "changeName_existingKeyPair_nameUpdatedInPlace" {
#   run "setUntrimmedName_existingKeyPair_configuredNamePreserved" {
#   run "planAgain_untrimmedName_stateStable" {
#   run "changePublicKey_existingKeyPair_keyPairReplaced" {
#   run "planAgain_customPublicKey_stateStable" {
#   run "addTrailingNewline_customPublicKey_publicKeyUpdatedInPlace" {
#   run "planAgain_publicKeyWithTrailingNewline_stateStable" {
#   run "omitPublicKey_publicKeySet_publicKeyPreservedInPlace" {
#   run "planAgain_omittedPublicKey_stateStable" {
#
# Chain import (state_key = "import_target"; plan-only import):
#   run "planImport_existingKeyPairId_stateRestored" {

provider "viettelcloud" {
  endpoint   = var.api_endpoint
  token      = var.token
  project_id = var.project_id
}

# Creates a generated SSH key pair and verifies all fields, including private_key.
run "createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned" {
  command = apply

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name = "tf-test-kp-lifecycle"
  }

  assert {
    condition = (
      length(viettelcloud_key_pair.key_pair.id) > 0 &&
      viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle" &&
      length(viettelcloud_key_pair.key_pair.public_key) > 0 &&
      length(viettelcloud_key_pair.key_pair.fingerprint) > 0 &&
      viettelcloud_key_pair.key_pair.type == "rsa" &&
      length(viettelcloud_key_pair.key_pair.private_key) > 0 &&
      length(viettelcloud_key_pair.key_pair.created_at) > 0 &&
      length(viettelcloud_key_pair.key_pair.updated_at) > 0 &&
      data.viettelcloud_key_pair.key_pair.id == viettelcloud_key_pair.key_pair.id &&
      data.viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle"
    )
    error_message = "Key pair was not created with the expected values."
  }
}

# Refreshes the existing key pair and asserts that state is preserved without changes.
run "refresh_existingKeyPair_statePreserved" {
  command = plan

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name = "tf-test-kp-lifecycle"
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_id &&
      viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle" &&
      viettelcloud_key_pair.key_pair.public_key == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_public_key &&
      viettelcloud_key_pair.key_pair.fingerprint == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_fingerprint &&
      viettelcloud_key_pair.key_pair.type == "rsa" &&
      viettelcloud_key_pair.key_pair.private_key == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_private_key
    )
    error_message = "Key pair state was not preserved during refresh."
  }
}

# Updates the key pair name in place.
run "changeName_existingKeyPair_nameUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name = "tf-test-kp-lifecycle-renamed"
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_id &&
      viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle-renamed" &&
      data.viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle-renamed" &&
      viettelcloud_key_pair.key_pair.public_key == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_public_key &&
      viettelcloud_key_pair.key_pair.fingerprint == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_fingerprint &&
      viettelcloud_key_pair.key_pair.private_key == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_private_key
    )
    error_message = "Key pair name was not updated in place."
  }
}

# Sets an untrimmed name and verifies that the configured value is preserved in state.
run "setUntrimmedName_existingKeyPair_configuredNamePreserved" {
  command = apply

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name = "  tf-test-kp-lifecycle-renamed  "
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_id &&
      viettelcloud_key_pair.key_pair.name == "  tf-test-kp-lifecycle-renamed  " &&
      data.viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle-renamed" &&
      viettelcloud_key_pair.key_pair.public_key == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_public_key &&
      viettelcloud_key_pair.key_pair.fingerprint == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_fingerprint &&
      viettelcloud_key_pair.key_pair.private_key == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_private_key
    )
    error_message = "Untrimmed key pair name was not preserved in state."
  }
}

# Asserts that a subsequent plan detects no drift for the untrimmed name.
run "planAgain_untrimmedName_stateStable" {
  command = plan

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name = "  tf-test-kp-lifecycle-renamed  "
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_id &&
      viettelcloud_key_pair.key_pair.name == "  tf-test-kp-lifecycle-renamed  " &&
      viettelcloud_key_pair.key_pair.public_key == run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_public_key
    )
    error_message = "Subsequent plan detected drift on untrimmed name."
  }
}

# Changing public_key requires replacement. Passes trailing whitespace to verify normalization and preservation.
run "changePublicKey_existingKeyPair_keyPairReplaced" {
  command = apply

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name       = "tf-test-kp-lifecycle-renamed"
    public_key = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test  "
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id != run.createKeyPair_nameSetAndPublicKeyOmitted_generatedKeyPairAndPrivateKeyReturned.key_pair_id &&
      viettelcloud_key_pair.key_pair.public_key == "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test  " &&
      viettelcloud_key_pair.key_pair.private_key == null
    )
    error_message = "Key pair was not replaced after changing public_key."
  }
}

# Asserts that a subsequent plan detects no drift for the user-supplied public key.
run "planAgain_customPublicKey_stateStable" {
  command = plan

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name       = "tf-test-kp-lifecycle-renamed"
    public_key = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test  "
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_id &&
      viettelcloud_key_pair.key_pair.public_key == "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test  " &&
      viettelcloud_key_pair.key_pair.private_key == null
    )
    error_message = "Subsequent plan detected drift on custom public key."
  }
}

# Adding a trailing newline, as file() does, names the same key and updates state in place.
run "addTrailingNewline_customPublicKey_publicKeyUpdatedInPlace" {
  command = apply

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name       = "tf-test-kp-lifecycle-renamed"
    public_key = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test\n"
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_id &&
      viettelcloud_key_pair.key_pair.public_key == "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test\n" &&
      viettelcloud_key_pair.key_pair.fingerprint == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_fingerprint &&
      viettelcloud_key_pair.key_pair.private_key == null
    )
    error_message = "Adding a trailing newline to public_key replaced the key pair or changed its fingerprint."
  }
}

# Asserts that a subsequent plan detects no drift for the public key with a trailing newline.
run "planAgain_publicKeyWithTrailingNewline_stateStable" {
  command = plan

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name       = "tf-test-kp-lifecycle-renamed"
    public_key = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test\n"
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_id &&
      viettelcloud_key_pair.key_pair.public_key == "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCYaHmUmaUHlt9gl3vklMrdWv0OUerISYy12QvEi0BjujzAm/yQDVdXM3hW+tKz0FvPWWAAz/NNvXlhcNwzKGI5K0BWGqxrYtrx/GrYhpgoJD9Jrh3NEVtEGa8HQWDQeq6KVcaU4x+Bt4GdArV2FqTKpwqU3/Y1Uzas+ZPy4j+9y5oD5+p7rJQEbEwd5eKn/owg6bYsFBJagzd1gG7OC4JFwCKN3YP+TZAlZ8/MZeU6eeu5ilVlY9bWFJxwfSjoN4z+ilMBuPedafzQdbaFdBos2QOPhdOVAlSzFQFWmqcQK0z4idZPRfHQf5KvPGOtcWYKfA433N8t4+iyhucLA+uF test@integration.test\n"
    )
    error_message = "Subsequent plan detected drift on the public key with a trailing newline."
  }
}

# Omitting an already configured public key preserves its backend value without replacement.
run "omitPublicKey_publicKeySet_publicKeyPreservedInPlace" {
  command = apply

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name = "tf-test-kp-lifecycle-renamed"
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_id &&
      viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle-renamed" &&
      viettelcloud_key_pair.key_pair.public_key == run.addTrailingNewline_customPublicKey_publicKeyUpdatedInPlace.key_pair_public_key &&
      viettelcloud_key_pair.key_pair.fingerprint == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_fingerprint &&
      viettelcloud_key_pair.key_pair.type == "rsa" &&
      viettelcloud_key_pair.key_pair.private_key == null &&
      data.viettelcloud_key_pair.key_pair.id == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_id &&
      data.viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle-renamed"
    )
    error_message = "Omitting public_key replaced the key pair or modified the public key."
  }
}

# Asserts that a subsequent plan detects no drift when public_key is omitted.
run "planAgain_omittedPublicKey_stateStable" {
  command = plan

  module {
    source = "./fixtures/key_pair/resource"
  }

  variables {
    name = "tf-test-kp-lifecycle-renamed"
  }

  assert {
    condition = (
      viettelcloud_key_pair.key_pair.id == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_id &&
      viettelcloud_key_pair.key_pair.name == "tf-test-kp-lifecycle-renamed" &&
      viettelcloud_key_pair.key_pair.public_key == run.addTrailingNewline_customPublicKey_publicKeyUpdatedInPlace.key_pair_public_key &&
      viettelcloud_key_pair.key_pair.fingerprint == run.changePublicKey_existingKeyPair_keyPairReplaced.key_pair_fingerprint &&
      viettelcloud_key_pair.key_pair.type == "rsa" &&
      viettelcloud_key_pair.key_pair.private_key == null
    )
    error_message = "Subsequent plan detected drift when public_key was omitted."
  }
}

# Plan-only import of the replaced key pair by ID, verifying schema attributes and null private key.
run "planImport_existingKeyPairId_stateRestored" {
  command   = plan
  state_key = "import_target"

  module {
    source = "./fixtures/key_pair/import"
  }

  variables {
    key_pair_id   = run.omitPublicKey_publicKeySet_publicKeyPreservedInPlace.key_pair_id
    key_pair_name = "tf-test-kp-lifecycle-renamed"
  }

  assert {
    condition = (
      viettelcloud_key_pair.imported.id == run.omitPublicKey_publicKeySet_publicKeyPreservedInPlace.key_pair_id &&
      viettelcloud_key_pair.imported.name == "tf-test-kp-lifecycle-renamed" &&
      viettelcloud_key_pair.imported.type == "rsa" &&
      viettelcloud_key_pair.imported.private_key == null &&
      length(viettelcloud_key_pair.imported.public_key) > 0 &&
      length(viettelcloud_key_pair.imported.fingerprint) > 0 &&
      length(viettelcloud_key_pair.imported.created_at) > 0 &&
      length(viettelcloud_key_pair.imported.updated_at) > 0
    )
    error_message = "Importing the key pair did not restore its expected state."
  }
}
