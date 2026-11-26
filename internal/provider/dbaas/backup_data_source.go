package dbaas

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	dbaaslookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/lookup"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var _ datasource.DataSource = &BackupDataSource{}

type BackupDataSource struct {
	client    *dbaassdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewBackupDataSource() datasource.DataSource { return &BackupDataSource{} }

func (d *BackupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_db_backup"
}

func (d *BackupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	lookup := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Optional: true, Computed: true, Description: description}
	}
	computed := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: description}
	}
	resp.Schema = schema.Schema{
		Description: "Find one manual or automatic backup of a Viettel Cloud DBaaS DB instance by ID or exact filters." +
			" name, db_instance_id, and backup_type together always select at most one backup.",
		Attributes: map[string]schema.Attribute{
			"id":                      lookup("Backup ID (UUID)."),
			"name":                    lookup("Exact backup name."),
			"db_instance_id":          lookup("Source DB instance ID (UUID). Null after the instance is deleted with keep_backup = true."),
			"backup_type":             lookup("Backup type: manual or automatic."),
			"status":                  lookup("Exact backup status, such as COMPLETED."),
			"database_type":           lookup("Database type."),
			"region":                  lookup("Exact region name."),
			"description":             computed("Description."),
			"db_instance_name":        computed("Source DB instance name."),
			"db_version":              computed("DB version of the source instance."),
			"db_version_id":           computed("DB version ID (UUID)."),
			"region_id":               computed("Region ID (UUID)."),
			"zone_id":                 computed("Zone ID (UUID), if any."),
			"backup_info":             computed("Backup details that the backend reports."),
			"size":                    schema.Int64Attribute{Computed: true, Description: "Backup size in MB."},
			"required_volume_size":    schema.Int64Attribute{Computed: true, Description: "Minimum volume size in GB to restore the backup."},
			"backup_timestamp":        computed("Time the backup completed (RFC3339)."),
			"start_restore_timestamp": computed("Earliest restore time (RFC3339), if any."),
			"end_restore_timestamp":   computed("Latest restore time (RFC3339), if any."),
			"backup_plan_id":          computed("Backup plan ID (UUID), if any."),
			"owner_id":                computed("Owner ID (UUID)."),
			"project_id":              computed("Owning project ID (UUID)."),
			"error_message":           computed("Last error that the backend reported."),
			"created_at":              computed("Creation timestamp (RFC3339)."),
			"updated_at":              computed("Last update timestamp (RFC3339)."),
		},
	}
}

func (d *BackupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	d.client, d.project, d.projectID = data.DBaaS, data.Project, data.ProjectID
}

func (d *BackupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config BackupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	backup, diags := d.getBackup(ctx, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || backup == nil {
		return
	}
	state := config
	populateBackupDataSourceState(backup, &state)
	preserveBackupConfiguredValues(backup, config, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (d *BackupDataSource) getBackup(ctx context.Context, config BackupDataSourceModel) (*dbaassdk.DBInstanceBackupSchema, diag.Diagnostics) {
	criteria := map[string]types.String{
		"id": config.ID, "name": config.Name, "db_instance_id": config.DBInstanceID, "backup_type": config.BackupType,
		"status": config.Status, "database_type": config.DatabaseType, "region": config.Region,
	}
	var diags diag.Diagnostics
	supplied := 0
	for name, value := range criteria {
		if value.IsNull() {
			continue
		}
		supplied++
		if value.IsUnknown() {
			diags.AddError("Unknown DB Instance Backup lookup criteria", name+" must be known.")
		} else if strings.TrimSpace(value.ValueString()) == "" {
			diags.AddError("Invalid DB Instance Backup lookup criteria", name+" must not be empty or whitespace-only.")
		}
	}
	if supplied == 0 {
		diags.AddError("Missing DB Instance Backup lookup criteria", "Specify id, name, db_instance_id, backup_type, status, database_type, or region.")
	}
	if diags.HasError() {
		return nil, diags
	}
	if supplied == 1 && !config.ID.IsNull() {
		id, idDiags := parse.UUIDString(config.ID, "id")
		diags.Append(idDiags...)
		if diags.HasError() {
			return nil, diags
		}
		return d.getBackupByID(ctx, id)
	}
	filter, filterDiags := d.backupFilter(ctx, config)
	diags.Append(filterDiags...)
	if diags.HasError() {
		return nil, diags
	}
	return d.getBackupByFilter(ctx, filter, config.ID)
}

func (d *BackupDataSource) getBackupByID(ctx context.Context, id core.UUID) (*dbaassdk.DBInstanceBackupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	backup, err := d.client.GetDbInstanceBackup(ctx, id, dbaassdk.GetDbInstanceBackupParams{ProjectID: d.projectID})
	if errors.Is(err, dbaassdk.ErrNotFound) {
		diags.AddError("DB instance backup not found", fmt.Sprintf("No DB instance backup found with id %s.", id))
	} else if err != nil {
		diags.AddError("Error reading DB instance backup", err.Error())
	}
	return backup, diags
}

// getBackupByFilter resolves the backup and then checks a
// configured id against the match, because the list API has no id filter.
func (d *BackupDataSource) getBackupByFilter(ctx context.Context, filter dbaaslookup.BackupFilter, configuredID types.String) (*dbaassdk.DBInstanceBackupSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	match, err := dbaaslookup.NewBackupFinder(d.client, d.projectID).Resolve(ctx, filter)
	if err != nil {
		diags.AddError("Unable to resolve DB instance backup", err.Error())
		return nil, diags
	}
	if !configuredID.IsNull() {
		id, idDiags := parse.UUIDString(configuredID, "id")
		diags.Append(idDiags...)
		if !diags.HasError() && id != match.Id {
			diags.AddError("Unable to resolve DB instance backup", fmt.Sprintf("The backup matching criteria (%s) has id %s, not %s.", filter, match.Id, id))
		}
	}
	return &match, diags
}

func (d *BackupDataSource) backupFilter(ctx context.Context, config BackupDataSourceModel) (dbaaslookup.BackupFilter, diag.Diagnostics) {
	var diags diag.Diagnostics
	var filter dbaaslookup.BackupFilter
	text := func(value types.String) *string {
		if !known(value) {
			return nil
		}
		return new(strings.TrimSpace(value.ValueString()))
	}
	filter.Name = text(config.Name)
	if value := text(config.BackupType); value != nil {
		filter.BackupType = new(dbaassdk.BackupType(*value))
	}
	if value := text(config.Status); value != nil {
		filter.Status = new(dbaassdk.BackupStatus(*value))
	}
	if value := text(config.DatabaseType); value != nil {
		filter.DatabaseType = new(dbaassdk.DatabaseType(*value))
	}
	if known(config.DBInstanceID) {
		id, idDiags := parse.UUIDString(config.DBInstanceID, "db_instance_id")
		diags.Append(idDiags...)
		filter.DBInstanceID = &id
	}
	if name := text(config.Region); name != nil {
		region, err := projectlookup.NewRegionFinder(d.project, d.projectID).Resolve(ctx, projectlookup.RegionFilter{Name: name})
		if err != nil {
			diags.AddError("Unable to resolve region", "region: "+err.Error())
			return filter, diags
		}
		filter.RegionID = &region.Region.Id
	}
	return filter, diags
}
