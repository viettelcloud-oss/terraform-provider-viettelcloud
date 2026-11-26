package dbaas

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &BackupResource{}
	_ resource.ResourceWithImportState    = &BackupResource{}
	_ resource.ResourceWithValidateConfig = &BackupResource{}
)

const (
	backupCreatePollInterval = 15 * time.Second
	backupCreateTimeout      = 60 * time.Minute
	backupDeletePollInterval = 10 * time.Second
	backupDeleteTimeout      = 30 * time.Minute
)

// backupReplaceWarning ends the description of every attribute
// whose change replaces the backup.
const backupReplaceWarning = " Changing it replaces the backup. Without create_before_destroy, Terraform deletes the old backup first," +
	" so a failed create loses it; the example sets lifecycle { create_before_destroy = true }."

type BackupResource struct {
	client    *dbaassdk.Client
	projectID core.UUID
}

func NewBackupResource() resource.Resource { return &BackupResource{} }

func (r *BackupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_db_backup"
}

func (r *BackupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Manage a manual backup of a Viettel Cloud DBaaS DB instance. The DB instance must be ACTIVE with no running operation." +
			" Backups of one instance cannot run at the same time, so chain them with depends_on. Automatic backups are read with the data source.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Backup ID (UUID).", PlanModifiers: keep},
			"name": schema.StringAttribute{
				Required: true, PlanModifiers: []planmodifier.String{replace.String(compare.SameTrimmedString)},
				Description: "Backup name, unique among the manual backups of the DB instance." + backupReplaceWarning,
			},
			"description": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keep,
				Description: "Description. Omit it to keep the current value; set an empty string to clear it.",
			},
			"db_instance": schema.SingleNestedAttribute{
				Required: true,
				Description: "Source DB instance." + backupReplaceWarning + " create_before_destroy also applies to the referenced viettelcloud_db_instance, so replace that instance together with a new name." +
					" An imported backup whose instance was deleted takes this ID from configuration without a replacement.",
				Attributes:    reference.Attributes(reference.ID, "DB instance ID (UUID)."),
				PlanModifiers: []planmodifier.Object{requiresReplaceIfOtherInstance()},
			},
			"db_instance_name":        schema.StringAttribute{Computed: true, Description: "Source DB instance name. Null after the instance is deleted with keep_backup = true."},
			"database_type":           schema.StringAttribute{Computed: true, Description: "Database type of the source instance."},
			"db_version":              schema.StringAttribute{Computed: true, Description: "DB version of the source instance."},
			"db_version_id":           schema.StringAttribute{Computed: true, Description: "DB version ID (UUID)."},
			"region":                  schema.StringAttribute{Computed: true, Description: "Region name."},
			"region_id":               schema.StringAttribute{Computed: true, Description: "Region ID (UUID)."},
			"zone_id":                 schema.StringAttribute{Computed: true, Description: "Zone ID (UUID), if any."},
			"backup_type":             schema.StringAttribute{Computed: true, Description: "Backup type; always manual for this resource."},
			"backup_info":             schema.StringAttribute{Computed: true, Description: "Backup details that the backend reports."},
			"size":                    schema.Int64Attribute{Computed: true, Description: "Backup size in MB."},
			"required_volume_size":    schema.Int64Attribute{Computed: true, Description: "Minimum volume size in GB to restore the backup."},
			"backup_timestamp":        schema.StringAttribute{Computed: true, Description: "Time the backup completed (RFC3339). Null while it is CREATING."},
			"start_restore_timestamp": schema.StringAttribute{Computed: true, Description: "Earliest restore time (RFC3339), if any."},
			"end_restore_timestamp":   schema.StringAttribute{Computed: true, Description: "Latest restore time (RFC3339), if any."},
			"backup_plan_id":          schema.StringAttribute{Computed: true, Description: "Backup plan ID (UUID), if any."},
			"owner_id":                schema.StringAttribute{Computed: true, Description: "Owner ID (UUID)."},
			"project_id":              schema.StringAttribute{Computed: true, Description: "Owning project ID (UUID).", PlanModifiers: keep},
			"status":                  schema.StringAttribute{Computed: true, Description: "Backup status: CREATING, COMPLETED, ERROR, or RESERVED."},
			"error_message":           schema.StringAttribute{Computed: true, Description: "Last error that the backend reported."},
			"created_at":              schema.StringAttribute{Computed: true, Description: "Creation timestamp (RFC3339).", PlanModifiers: keep},
			"updated_at":              schema.StringAttribute{Computed: true, Description: "Last update timestamp (RFC3339)."},
		},
	}
}

func (r *BackupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client, r.projectID = data.DBaaS, data.ProjectID
}

func (r *BackupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config BackupResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateNotBlank(config.Name, path.Root("name"))...)
	if id := reference.Get(config.DBInstance, reference.ID); known(id) {
		_, diags := parse.UUIDString(id, "db_instance.id")
		resp.Diagnostics.Append(diags...)
	}
}

func (r *BackupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BackupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	instanceID, diags := parse.UUIDString(reference.Get(plan.DBInstance, reference.ID), "db_instance.id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := dbaassdk.DBInstanceBackupCreateSchema{DbInstanceId: instanceID, Name: trimmed(plan.Name)}
	if known(plan.Description) {
		body.Description = new(trimmed(plan.Description))
	}
	backup, err := r.client.CreateDbInstanceBackup(ctx, dbaassdk.CreateDbInstanceBackupParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating DB instance backup", err.Error())
		return
	}
	// Save the ID before polling, so that a failed wait leaves a tainted backup
	// in state instead of an untracked one whose name blocks a retry.
	populateBackupResourceState(backup, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := backup.Id
	backup, err = r.waitUntilCompleted(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for DB instance backup creation", fmt.Sprintf("%s\n\nBackup ID %s was preserved in state.", err, id))
		return
	}
	populateBackupResourceState(backup, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refuses an automatic backup. The backend creates those from the backup
// policy of the instance and refuses to delete them while the instance exists,
// so this resource cannot manage one. Import reads too, so it refuses as well.
func (r *BackupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BackupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	backup, err := r.get(ctx, id)
	if errors.Is(err, dbaassdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading DB instance backup", err.Error())
		return
	}
	if backup.BackupType != dbaassdk.BackupTypeManual {
		resp.Diagnostics.AddError("Unsupported DB instance backup type",
			fmt.Sprintf("Backup %s is %s. viettelcloud_db_backup manages only manual backups; read automatic backups with the data source.", id, backup.BackupType))
		return
	}
	populateBackupResourceState(backup, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *BackupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state BackupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The update is synchronous and returns the whole backup, so no read follows.
	var backup *dbaassdk.DBInstanceBackupSchema
	var err error
	if body, changed := buildBackupUpdateBody(plan, state); changed {
		backup, err = r.client.PartialUpdateDbInstanceBackup(ctx, id, dbaassdk.PartialUpdateDbInstanceBackupParams{ProjectID: r.projectID}, body)
	} else {
		backup, err = r.get(ctx, id)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating DB instance backup", err.Error())
		return
	}
	newState := plan
	populateBackupResourceState(backup, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *BackupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state BackupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteDbInstanceBackup(ctx, id, dbaassdk.DeleteDbInstanceBackupParams{ProjectID: r.projectID})
	if errors.Is(err, dbaassdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting DB instance backup", err.Error())
		return
	}
	priorStatus := dbaassdk.BackupStatus(state.Status.ValueString())
	if err := r.waitUntilDeleted(ctx, id, priorStatus); err != nil {
		resp.Diagnostics.AddError("Error waiting for DB instance backup deletion", err.Error())
	}
}

func (r *BackupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if _, diags := parse.UUIDString(types.StringValue(id), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func (r *BackupResource) get(ctx context.Context, id core.UUID) (*dbaassdk.DBInstanceBackupSchema, error) {
	return r.client.GetDbInstanceBackup(ctx, id, dbaassdk.GetDbInstanceBackupParams{ProjectID: r.projectID})
}

// waitUntilCompleted polls until the backup completes. The backend copies the
// raw core status during sync, so a status outside the documented set is
// treated as not ready.
func (r *BackupResource) waitUntilCompleted(ctx context.Context, id core.UUID) (*dbaassdk.DBInstanceBackupSchema, error) {
	w := wait.New[*dbaassdk.DBInstanceBackupSchema](backupCreatePollInterval, backupCreateTimeout)
	backup, err := w.WaitFor(ctx, func(ctx context.Context) (*dbaassdk.DBInstanceBackupSchema, error) {
		backup, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		if backup.Status == dbaassdk.BackupStatusCOMPLETED {
			return backup, nil
		}
		if backup.Status == dbaassdk.BackupStatusERROR {
			return nil, fmt.Errorf("status is ERROR: %s", backup.ErrorMessage)
		}
		return nil, fmt.Errorf("status is %s: %w", backup.Status, wait.ErrNotReady)
	})
	if err != nil {
		return nil, fmt.Errorf("DB instance backup %s: %w", id, err)
	}
	return backup, nil
}

// waitUntilDeleted polls until the backup is gone. A failed delete sets
// status ERROR, which is indistinguishable from a backup that was ERROR before
// the delete. Only a backup that was not ERROR before therefore stops on it.
func (r *BackupResource) waitUntilDeleted(ctx context.Context, id core.UUID, priorStatus dbaassdk.BackupStatus) error {
	w := wait.New[struct{}](backupDeletePollInterval, backupDeleteTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		backup, err := r.get(ctx, id)
		if errors.Is(err, dbaassdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		if backup.Status == dbaassdk.BackupStatusERROR && priorStatus != dbaassdk.BackupStatusERROR {
			return struct{}{}, fmt.Errorf("deletion failed: %s", backup.ErrorMessage)
		}
		return struct{}{}, fmt.Errorf("status is %s, error_message %q: %w", backup.Status, backup.ErrorMessage, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("DB instance backup %s: %w", id, err)
	}
	return nil
}

// buildBackupUpdateBody sends description when it changes. name is
// never sent: a new name replaces the backup by product decision, although the
// backend could rename it.
func buildBackupUpdateBody(plan, state BackupResourceModel) (dbaassdk.DBInstanceBackupPartialUpdateSchema, bool) {
	var body dbaassdk.DBInstanceBackupPartialUpdateSchema
	if known(plan.Description) && !compare.SameTrimmedString(plan.Description, state.Description) {
		body.Description = new(trimmed(plan.Description))
	}
	return body, body.Description != nil
}

// requiresReplaceIfOtherInstance replaces the backup when db_instance names
// another instance. A null prior db_instance comes only from importing a
// backup whose instance was deleted, which the response no longer names. The
// configured ID is then adopted, because a replacement would delete the backup
// and then fail to create one for the missing instance.
func requiresReplaceIfOtherInstance() planmodifier.Object {
	return objectplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !req.StateValue.IsNull() &&
				!compare.SameUUIDString(reference.Get(req.PlanValue, reference.ID), reference.Get(req.StateValue, reference.ID))
		},
		"Referencing another DB instance replaces the backup.",
		"Referencing another DB instance replaces the backup.",
	)
}
