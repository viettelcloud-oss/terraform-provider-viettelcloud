package blockstorage

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

// volume_backup_policy_attachment.go owns the backup_policy attribute of
// viettelcloud_volume: its schema, the create-time ban, the attach, replace,
// and detach request, and the refresh from the volume response.

// volumeBackupPolicyAttribute describes backup_policy. The upgrade and ownership
// warnings of design/blockstorage/volume.md, section 4.3, live in
// templates/resources/volume.md.tmpl, so the Description stays one clause.
func volumeBackupPolicyAttribute() schema.Attribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "Backup policy to attach to the volume. Removing the attribute detaches the policy.",
		Attributes:  reference.Attributes(reference.ID, "Backup policy ID (UUID)."),
	}
}

// validateVolumeBackupPolicyConfig requires a configured backup_policy.id to be
// a UUID after the trim. An unknown value passes, because it is not decided
// yet.
func validateVolumeBackupPolicyConfig(policy types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	id := reference.Get(policy, reference.ID)
	if policy.IsNull() || id.IsUnknown() {
		return diags
	}
	_, idDiags := parseBackupPolicyUUID(id, "backup_policy.id")
	diags.Append(idDiags...)
	return diags
}

// validateVolumeBackupPolicyUnset rejects backup_policy on a volume that is
// being created or replaced. Create sends no policy request, so the attribute
// would stay in the plan and then be missing from the new volume.
func validateVolumeBackupPolicyUnset(policy types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if policy.IsNull() || policy.IsUnknown() {
		return diags
	}
	diags.AddAttributeError(
		path.Root("backup_policy"),
		"Invalid volume policy",
		"backup_policy cannot be set while the volume is created or replaced. Create the volume first, then add backup_policy in a later apply.",
	)
	return diags
}

// buildVolumeBackupPolicyBody returns the request that moves the volume from
// the policy in state to the policy in the plan, and false when the two name
// the same UUID. A detach sends JSON null, because an unset core.Nullable
// marshals a zero UUID and the API would read that as a policy ID.
func buildVolumeBackupPolicyBody(plan, state types.Object) (blockstoragesdk.VolumeConfigureBackupPolicySchema, bool, diag.Diagnostics) {
	var body blockstoragesdk.VolumeConfigureBackupPolicySchema
	var diags diag.Diagnostics

	planned := reference.Get(plan, reference.ID)
	current := reference.Get(state, reference.ID)
	if planned.IsUnknown() {
		diags.AddError("Invalid volume policy", "backup_policy.id must be known before the volume is updated.")
		return body, false, diags
	}
	if planned.IsNull() {
		if current.IsNull() {
			return body, false, diags
		}
		body.BackupPolicyId.SetNull()
		return body, true, diags
	}

	id, idDiags := parseBackupPolicyUUID(planned, "backup_policy.id")
	diags.Append(idDiags...)
	if diags.HasError() {
		return body, false, diags
	}
	if compare.SameUUID(current, id) {
		return body, false, diags
	}
	body.BackupPolicyId.Set(id)
	return body, true, diags
}

// updateVolumeBackupPolicy attaches, replaces, or detaches the backup policy of
// the volume. The request returns at once and does not change the volume
// status, so there is nothing to wait for.
func (r *VolumeResource) updateVolumeBackupPolicy(ctx context.Context, volumeID core.UUID, plan, state types.Object) diag.Diagnostics {
	body, changed, diags := buildVolumeBackupPolicyBody(plan, state)
	if diags.HasError() || !changed {
		return diags
	}
	if err := r.client.ConfigureBackupPolicy(ctx, volumeID, blockstoragesdk.ConfigureBackupPolicyParams{ProjectID: r.projectID}, body); err != nil {
		diags.AddError("Error updating backup policy", fmt.Sprintf("Volume %s: %s", volumeID, err))
	}
	return diags
}

// populateVolumeBackupPolicy refreshes backup_policy from the volume response.
// A policy that the response reports keeps the configured spelling of its ID,
// outer whitespace included, when both name the same UUID. A response without
// a policy maps to null.
func populateVolumeBackupPolicy(volume *blockstoragesdk.VolumeSchema, state *VolumeResourceModel) {
	policy := core.NullableToPtr(volume.BackupPolicy)
	if policy == nil {
		state.BackupPolicy = reference.Value(reference.ID, types.StringNull())
		return
	}
	configured := reference.Get(state.BackupPolicy, reference.ID)
	state.BackupPolicy = reference.Value(reference.ID, compare.PreservedUUID(configured, policy.Id))
}
