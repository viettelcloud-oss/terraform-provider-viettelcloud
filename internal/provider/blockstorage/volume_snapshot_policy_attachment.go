package blockstorage

// volume_snapshot_policy_attachment.go owns the snapshot_policy attribute of
// viettelcloud_volume: its schema, the rejection at create and replace, the
// refresh mapping, and the request that attaches, switches, or detaches the
// policy.

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

func volumeSnapshotPolicyAttribute() schema.Attribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "Snapshot policy to attach to the volume. Removing the attribute detaches the policy.",
		Attributes:  reference.Attributes(reference.ID, "Snapshot policy ID (UUID)."),
	}
}

// validateVolumeSnapshotPolicyConfig makes sure that a configured policy ID is a
// UUID at plan time. An unknown ID is resolved at apply.
func validateVolumeSnapshotPolicyConfig(policy types.Object) diag.Diagnostics {
	id := reference.Get(policy, reference.ID)
	if policy.IsNull() || id.IsUnknown() {
		return nil
	}
	_, diags := parse.UUIDString(snapshotPolicyTrimmed(id), "snapshot_policy.id")
	return diags
}

// validateVolumeSnapshotPolicyAbsent rejects a policy while the volume is
// created or replaced. The attach call needs an existing volume in a ready
// state, so the provider attaches in the update flow only. A reference that is
// unknown at plan time is rejected too, because it cannot be proven null.
func validateVolumeSnapshotPolicyAbsent(policy types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if policy.IsNull() {
		return diags
	}
	diags.AddAttributeError(
		path.Root("snapshot_policy"),
		"Invalid volume policy",
		"snapshot_policy cannot be set while the volume is created or replaced. Create the volume first, then add snapshot_policy in a later apply.",
	)
	return diags
}

// volumeSnapshotPolicyState maps the policy that the volume response reports.
// A policy that names the same ID as the prior value keeps the configured
// spelling of that ID, outer whitespace included.
func volumeSnapshotPolicyState(volume *blockstoragesdk.VolumeSchema, prior types.Object) types.Object {
	policy := core.NullableToPtr(volume.SnapshotPolicy)
	if policy == nil {
		return reference.Value(reference.ID, types.StringNull())
	}
	if configured := reference.Get(prior, reference.ID); compare.SameUUID(snapshotPolicyTrimmed(configured), policy.Id) {
		return reference.Value(reference.ID, configured)
	}
	return reference.Value(reference.ID, types.StringValue(policy.Id.String()))
}

// buildVolumeSnapshotPolicyUpdate decides the policy request of an update.
// Detaching sends JSON null: an unset Nullable is encoded as the zero UUID, so
// SetNull is required. The bool is false when the plan leaves the policy as it
// is.
func buildVolumeSnapshotPolicyUpdate(planned, current types.Object) (blockstoragesdk.VolumeSnapshotPolicyUpdateSchema, bool, diag.Diagnostics) {
	var body blockstoragesdk.VolumeSnapshotPolicyUpdateSchema
	if planned.IsNull() {
		if current.IsNull() {
			return body, false, nil
		}
		body.SnapshotPolicyId.SetNull()
		return body, true, nil
	}
	id, diags := parse.UUIDString(snapshotPolicyTrimmed(reference.Get(planned, reference.ID)), "snapshot_policy.id")
	if diags.HasError() {
		return body, false, diags
	}
	if compare.SameUUID(snapshotPolicyTrimmed(reference.Get(current, reference.ID)), id) {
		return body, false, nil
	}
	body.SnapshotPolicyId.Set(id)
	return body, true, nil
}

// applyVolumeSnapshotPolicy runs after the patch, retype, and extend steps,
// because the API attaches a policy only to a volume that is available or in
// use. The call returns at once and does not change the volume status, so no
// waiter follows.
func (r *VolumeResource) applyVolumeSnapshotPolicy(ctx context.Context, volumeID core.UUID, planned, current types.Object) diag.Diagnostics {
	body, changed, diags := buildVolumeSnapshotPolicyUpdate(planned, current)
	if diags.HasError() || !changed {
		return diags
	}
	err := r.client.SetVolumeSnapshotPolicy(ctx, volumeID, blockstoragesdk.SetVolumeSnapshotPolicyParams{ProjectID: r.projectID}, body)
	if err != nil {
		diags.AddError("Error updating snapshot policy", err.Error())
	}
	return diags
}
