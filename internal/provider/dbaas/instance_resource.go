package dbaas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	dbaaslookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/lookup"
	dbaasselector "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas/selector"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/replace"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/selector"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

var (
	_ resource.Resource                   = &InstanceResource{}
	_ resource.ResourceWithImportState    = &InstanceResource{}
	_ resource.ResourceWithValidateConfig = &InstanceResource{}
	_ resource.ResourceWithModifyPlan     = &InstanceResource{}
)

const (
	instancePollInterval  = 15 * time.Second
	instanceCreateTimeout = 60 * time.Minute
	instanceDeleteTimeout = 30 * time.Minute
)

// instanceReplaceWarning ends the description of every attribute whose
// change replaces the DB instance.
const instanceReplaceWarning = " Changing it replaces the DB instance, which destroys its data." +
	" When a viettelcloud_db_backup with create_before_destroy references this instance, change name in the same apply," +
	" because the new instance is created before the old one is destroyed and names are unique in a project."

type InstanceResource struct {
	client    *dbaassdk.Client
	network   *networksdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewInstanceResource() resource.Resource { return &InstanceResource{} }

func (r *InstanceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_db_instance"
}

func (r *InstanceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceTrimmed := []planmodifier.String{replace.String(compare.SameTrimmedString)}
	keepString := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	keepInt := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	keepBool := []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud DBaaS DB instance. A replacement destroys the data of the instance; consider lifecycle { prevent_destroy = true }." +
			" Terraform cannot destroy an instance that the backend reports as ABNORMAL, unless its operational_status is FAILED: resolve it in the console, then run terraform state rm or wait for ACTIVE or ERROR.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, Description: "DB instance ID (UUID).", PlanModifiers: keepString},
			"name": schema.StringAttribute{Required: true, Description: "DB instance name, unique in the project." + instanceReplaceWarning, PlanModifiers: replaceTrimmed},
			"description": schema.StringAttribute{
				Optional: true, Computed: true, PlanModifiers: keepString,
				Description: "Description. Omit it to keep the current value; set an empty string to clear it.",
			},
			"database_type": schema.StringAttribute{Required: true, Description: "Database type: mysql, postgresql, mariadb, or redis." + instanceReplaceWarning, PlanModifiers: replaceTrimmed},
			"db_version": dbaasselector.DBVersionAttribute("DB version selector for the database type and region. Changed criteria keep the current version when it still matches." +
				" Selecting a different version replaces the DB instance, which destroys its data."),
			"region": schema.SingleNestedAttribute{
				Required: true, Description: "Region of the DB instance." + instanceReplaceWarning,
				Attributes:    reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{replace.Reference(reference.Name, compare.SameTrimmedString)},
			},
			"region_id": schema.StringAttribute{Computed: true, Description: "Region ID (UUID).", PlanModifiers: keepString},
			"flavor": dbaasselector.FlavorAttribute("Flavor selector in the region. Changed criteria keep the current flavor when it still matches." +
				" Selecting a different flavor replaces the DB instance, which destroys its data."),
			"flavor_vcpus": schema.Int64Attribute{Computed: true, Description: "vCPUs of the flavor."},
			"flavor_ram":   schema.Int64Attribute{Computed: true, Description: "RAM of the flavor in MB."},
			"volume_type": dbaasselector.VolumeTypeAttribute("Volume type selector in the region. Changed criteria keep the current volume type when it still matches." +
				" Selecting a different volume type replaces the DB instance, which destroys its data."),
			"volume_type_disk_type": schema.StringAttribute{Computed: true, Description: "Disk type of the volume type: nvme, ssd, sas, or hdd."},
			"disk_size": schema.Int64Attribute{
				Required: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Description: "Disk size in GB that creation requests. Refresh keeps this value; the provisioned size, which auto-increase can grow, is current_disk_size." +
					" After an import, set it to current_disk_size." + instanceReplaceWarning,
			},
			"current_disk_size": schema.Int64Attribute{Computed: true, Description: "Disk size in GB that the backend has provisioned, including any automatic increase."},
			"used_disk_size":    schema.Int64Attribute{Computed: true, Description: "Used disk space in MB."},
			"disk_full":         schema.BoolAttribute{Computed: true, Description: "Whether the disk is full."},
			"solution":          schema.StringAttribute{Required: true, Description: "Solution: basic or business." + instanceReplaceWarning, PlanModifiers: replaceTrimmed},
			"subnet": schema.SingleNestedAttribute{
				Required: true, Description: "Existing subnet in a VPC of the same region." + instanceReplaceWarning,
				Attributes:    reference.Attributes(reference.ID, "Subnet ID (UUID)."),
				PlanModifiers: []planmodifier.Object{replace.Reference(reference.ID, compare.SameUUIDString)},
			},
			"subnet_name": schema.StringAttribute{Computed: true, Description: "Subnet name."},
			"subnet_cidr": schema.StringAttribute{Computed: true, Description: "Subnet CIDR."},
			"vpc_id":      schema.StringAttribute{Computed: true, Description: "VPC ID (UUID) of the subnet."},
			"vpc_name":    schema.StringAttribute{Computed: true, Description: "VPC name of the subnet."},
			"root_password": schema.StringAttribute{
				Required: true, WriteOnly: true, Sensitive: true,
				Description: "Root password, sent only at creation and never stored in state. Changing it later has no effect and plans no change; change the password in the console.",
			},
			"enable_public_access": schema.BoolAttribute{
				Optional: true, Computed: true,
				Description:   "Whether the instance has a public endpoint. Defaults to false at creation." + instanceReplaceWarning,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplaceIfConfigured()},
			},
			"maintenance_config": schema.SingleNestedAttribute{
				Computed: true, Description: "Maintenance window that the backend assigns.",
				Attributes: map[string]schema.Attribute{
					"day_of_week": schema.StringAttribute{Computed: true, Description: "Lowercase day name, such as sunday."},
					"start_hour":  schema.Int64Attribute{Computed: true, Description: "Start hour, 0 to 23."},
				},
			},
			"keep_backup": schema.BoolAttribute{
				Optional: true, Computed: true, PlanModifiers: keepBool,
				Description: "Whether backups remain after the instance is deleted. Omit it to keep the current value. A new instance always starts with true, so false is accepted only on an existing instance.",
			},
			"backup_policy": schema.SingleNestedAttribute{
				Optional: true, Computed: true, PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Description: "Automatic backup policy. Omit it to keep the current policy. A new instance has automatic backups disabled.",
				Attributes: map[string]schema.Attribute{
					"enabled": schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: keepBool, Description: "Whether automatic backups run. Required when backup_policy is configured."},
					"backup_interval": schema.StringAttribute{
						Optional: true, Computed: true, PlanModifiers: keepString,
						Description: "daily or weekly. Required when enabled is true; rejected when it is false.",
					},
					"day_of_week": schema.StringAttribute{
						Optional: true, Computed: true, PlanModifiers: keepString,
						Description: "Lowercase day name. Required when backup_interval is weekly; rejected otherwise.",
					},
					"start_hour":      schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt, Description: "Start hour, 0 to 23. Required when enabled is true; rejected when it is false."},
					"max_auto_backup": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt, Description: "Automatic backups kept, 7 to 180. Required when enabled is true; rejected when it is false."},
				},
			},
			"auto_increase_volume_size": schema.SingleNestedAttribute{
				Optional: true, Computed: true, PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Description: "Automatic disk increase. Omit it to keep the current setting. A new instance starts with it disabled, so enabled = true is accepted only on an existing instance.",
				Attributes: map[string]schema.Attribute{
					"enabled":   schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: keepBool, Description: "Whether the disk grows automatically. Required when auto_increase_volume_size is configured."},
					"threshold": schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt, Description: "Used-space percentage that triggers an increase, 50 to 95. Required when enabled is true; rejected when it is false."},
					"limit":     schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: keepInt, Description: "Maximum disk size in GB, 20 to 3000 in steps of 10. Required when enabled is true; rejected when it is false."},
				},
			},
			"allowed_cidrs":      schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "CIDRs allowed to connect."},
			"enable_ssl":         schema.BoolAttribute{Computed: true, Description: "Whether SSL is enabled."},
			"private_url":        schema.StringAttribute{Computed: true, Description: "Private endpoint host. Empty until the backend first syncs the instance."},
			"public_url":         schema.StringAttribute{Computed: true, Description: "Public endpoint host."},
			"port":               schema.Int64Attribute{Computed: true, Description: "Database port."},
			"init_backup_id":     schema.StringAttribute{Computed: true, Description: "ID (UUID) of the backup that the instance was restored from, if any."},
			"project_id":         schema.StringAttribute{Computed: true, Description: "Owning project ID (UUID).", PlanModifiers: keepString},
			"status":             schema.StringAttribute{Computed: true, Description: "Instance status, such as ACTIVE."},
			"operational_status": schema.StringAttribute{Computed: true, Description: "Running operation, such as NOOP or DELETING."},
			"error_message":      schema.StringAttribute{Computed: true, Description: "Last error that the backend reported."},
			"created_at":         schema.StringAttribute{Computed: true, Description: "Creation timestamp (RFC3339).", PlanModifiers: keepString},
			"updated_at":         schema.StringAttribute{Computed: true, Description: "Last update timestamp (RFC3339)."},
		},
	}
}

func (r *InstanceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client, r.network, r.project, r.projectID = data.DBaaS, data.Network, data.Project, data.ProjectID
}

func (r *InstanceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config InstanceResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateNotBlank(config.Name, path.Root("name"))...)
	if known(config.DatabaseType) && !dbaassdk.DatabaseType(trimmed(config.DatabaseType)).Valid() {
		resp.Diagnostics.AddAttributeError(path.Root("database_type"), "Invalid database type", "database_type must be mysql, postgresql, mariadb, or redis.")
	}
	if known(config.Solution) && !dbaassdk.Solution(trimmed(config.Solution)).Valid() {
		resp.Diagnostics.AddAttributeError(path.Root("solution"), "Invalid solution", "solution must be basic or business.")
	}
	resp.Diagnostics.Append(validateNotBlank(reference.Get(config.Region, reference.Name), path.Root("region").AtName(reference.Name))...)
	if subnetID := reference.Get(config.Subnet, reference.ID); known(subnetID) {
		_, diags := parse.UUIDString(subnetID, "subnet.id")
		resp.Diagnostics.Append(diags...)
	}
	resp.Diagnostics.Append(validateBackupPolicyConfig(ctx, config.BackupPolicy)...)
	resp.Diagnostics.Append(validateAutoIncreaseConfig(ctx, config.AutoIncreaseVolumeSize)...)
}

// ModifyPlan decides catalog replacement from selector identity and rejects
// a create whose configuration the create request cannot express.
//
// resp.RequiresReplace cannot show the replacements that the attribute plan
// modifiers report, because the framework adds them only after this method
// returns. The immutable attributes are therefore compared directly.
func (r *InstanceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, config InstanceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	creating := req.State.Raw.IsNull()
	if !creating {
		var state InstanceResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		selectorReplaced := r.planSelectors(ctx, resp, plan, state)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(planDayOfWeek(ctx, resp, config, plan, state)...)
		creating = selectorReplaced || instanceImmutableChanged(plan, state, config)
	}
	if creating {
		resp.Diagnostics.Append(validateCreateDefaults(ctx, config)...)
	}
}

func (r *InstanceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan InstanceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	var rootPassword types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("root_password"), &rootPassword)...)
	if resp.Diagnostics.HasError() {
		return
	}
	regionID, diags := r.resolveRegion(ctx, plan.Region)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.validateSubnet(ctx, plan.Subnet, regionID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := buildInstanceCreateBody(ctx, &plan, rootPassword, regionID, r.client)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	instance, err := r.client.CreateDbInstance(ctx, dbaassdk.CreateDbInstanceParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating DB instance", err.Error())
		return
	}
	// Save the ID before polling, so that a failed wait leaves a tainted
	// instance in state instead of an untracked one whose name blocks a retry.
	resp.Diagnostics.Append(populateInstanceResourceState(instance, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := instance.Id
	instance, err = r.waitUntilActive(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error waiting for DB instance creation", fmt.Sprintf("%s\n\nDB instance ID %s was preserved in state.", err, id))
		return
	}
	resp.Diagnostics.Append(populateInstanceResourceState(instance, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *InstanceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state InstanceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	instance, err := r.get(ctx, id)
	if errors.Is(err, dbaassdk.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading DB instance", err.Error())
		return
	}
	resp.Diagnostics.Append(populateInstanceResourceState(instance, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *InstanceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state InstanceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	body, changed, diags := buildInstanceUpdateBody(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if changed {
		if err := r.client.UpdateDbInstance(ctx, id, dbaassdk.UpdateDbInstanceParams{ProjectID: r.projectID}, body); err != nil {
			resp.Diagnostics.AddError("Error updating DB instance", err.Error())
			return
		}
	}
	// The update returns no body. The backend writes the new values before it
	// responds, so one read returns them; the core call has no status to wait for.
	instance, err := r.get(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading DB instance after update", err.Error())
		return
	}
	newState := plan
	resp.Diagnostics.Append(populateInstanceResourceState(instance, &newState)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *InstanceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state InstanceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteDbInstance(ctx, id, dbaassdk.DeleteDbInstanceParams{ProjectID: r.projectID})
	if errors.Is(err, dbaassdk.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting DB instance", err.Error())
		return
	}
	if err := r.waitUntilDeleted(ctx, id); err != nil {
		resp.Diagnostics.AddError("Error waiting for DB instance deletion", err.Error())
	}
}

func (r *InstanceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if _, diags := parse.UUIDString(types.StringValue(id), "id"); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func (r *InstanceResource) get(ctx context.Context, id core.UUID) (*dbaassdk.DBInstanceSchema, error) {
	return r.client.GetDbInstance(ctx, id, dbaassdk.GetDbInstanceParams{ProjectID: r.projectID})
}

func (r *InstanceResource) resolveRegion(ctx context.Context, region types.Object) (core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := reference.Get(region, reference.Name)
	if !known(name) || strings.TrimSpace(name.ValueString()) == "" {
		diags.AddError("Missing region", "region.name must be configured and known.")
		return core.UUID{}, diags
	}
	value := trimmed(name)
	resolved, err := projectlookup.NewRegionFinder(r.project, r.projectID).Resolve(ctx, projectlookup.RegionFilter{Name: &value})
	if err != nil {
		diags.AddError("Unable to resolve region", "region.name: "+err.Error())
		return core.UUID{}, diags
	}
	return resolved.Region.Id, diags
}

// validateSubnet checks that the subnet exists in the project and belongs to
// a VPC in the region of the DB instance, because the instance does not create
// its network.
func (r *InstanceResource) validateSubnet(ctx context.Context, subnet types.Object, regionID core.UUID) diag.Diagnostics {
	subnetID, diags := parse.UUIDString(reference.Get(subnet, reference.ID), "subnet.id")
	if diags.HasError() {
		return diags
	}
	found, err := r.network.GetSubnet(ctx, subnetID, networksdk.GetSubnetParams{ProjectID: r.projectID})
	if errors.Is(err, networksdk.ErrNotFound) {
		diags.AddAttributeError(path.Root("subnet").AtName(reference.ID), "Subnet not found", fmt.Sprintf("No subnet %s exists in the project.", subnetID))
		return diags
	}
	if err != nil {
		diags.AddError("Error reading subnet", err.Error())
		return diags
	}
	if found.Region.Id != regionID {
		diags.AddAttributeError(path.Root("subnet").AtName(reference.ID), "Subnet in another region",
			fmt.Sprintf("Subnet %s is in region %q, but the DB instance is in another region.", subnetID, found.Region.Name))
	}
	return diags
}

// planSelectors keeps each catalog entry that the planned criteria still
// select, and reports whether any selector replaces the instance. A changed
// region or database type replaces the instance anyway, so its catalog is not
// read.
func (r *InstanceResource) planSelectors(ctx context.Context, resp *resource.ModifyPlanResponse, plan, state InstanceResourceModel) bool {
	regionID, regionErr := core.ParseUUID(state.RegionID.ValueString())
	scopeChanged := regionErr != nil || !sameRegion(plan.Region, state.Region) || !compare.SameTrimmedString(plan.DatabaseType, state.DatabaseType)
	replaced := false
	decide := func(field string, planned, prior types.Object, find func(context.Context) ([]core.UUID, error), current func(*dbaassdk.DBInstanceSchema) core.UUID) {
		p := path.Root(field)
		decision, id := selector.Different, types.StringUnknown()
		if !scopeChanged {
			var diags diag.Diagnostics
			decision, id, diags = selector.Decide(ctx, planned, prior, field, find, func(ctx context.Context) (core.UUID, error) {
				instanceID, err := core.ParseUUID(state.ID.ValueString())
				if err != nil {
					return core.UUID{}, err
				}
				instance, err := r.get(ctx, instanceID)
				if err != nil {
					return core.UUID{}, err
				}
				return current(instance), nil
			})
			resp.Diagnostics.Append(diags...)
		}
		if decision != selector.Same {
			replaced = true
			resp.RequiresReplace = append(resp.RequiresReplace, p)
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, p, selector.WithID(planned, id))...)
	}
	decide("db_version", plan.DBVersion, state.DBVersion, func(ctx context.Context) ([]core.UUID, error) {
		filter, diags := dbaasselector.DBVersionFilter(plan.DBVersion, "db_version", dbaassdk.DatabaseType(trimmed(plan.DatabaseType)), regionID)
		if diags.HasError() {
			return nil, fmt.Errorf("%v", diags)
		}
		candidates, err := dbaaslookup.NewDBVersionFinder(r.client).Find(ctx, filter)
		return collectIDs(candidates, func(v dbaassdk.DBaaSVersionSchema) core.UUID { return v.Id }), err
	}, func(instance *dbaassdk.DBInstanceSchema) core.UUID { return instance.DbVersion.Id })
	decide("flavor", plan.Flavor, state.Flavor, func(ctx context.Context) ([]core.UUID, error) {
		filter, diags := dbaasselector.FlavorFilter(plan.Flavor, "flavor", regionID)
		if diags.HasError() {
			return nil, fmt.Errorf("%v", diags)
		}
		candidates, err := dbaaslookup.NewFlavorFinder(r.client).Find(ctx, filter)
		return collectIDs(candidates, func(v dbaassdk.DBaaSRegionFlavorSchema) core.UUID { return v.Id }), err
	}, func(instance *dbaassdk.DBInstanceSchema) core.UUID { return instance.Flavor.Id })
	decide("volume_type", plan.VolumeType, state.VolumeType, func(ctx context.Context) ([]core.UUID, error) {
		filter, diags := dbaasselector.VolumeTypeFilter(plan.VolumeType, "volume_type", regionID)
		if diags.HasError() {
			return nil, fmt.Errorf("%v", diags)
		}
		candidates, err := dbaaslookup.NewVolumeTypeFinder(r.client).Find(ctx, filter)
		return collectIDs(candidates, func(v dbaassdk.DBaaSRegionVolumeTypeSchema) core.UUID { return v.Id }), err
	}, func(instance *dbaassdk.DBInstanceSchema) core.UUID { return instance.VolumeType.Id })
	return replaced
}

func (r *InstanceResource) waitUntilActive(ctx context.Context, id core.UUID) (*dbaassdk.DBInstanceSchema, error) {
	w := wait.New[*dbaassdk.DBInstanceSchema](instancePollInterval, instanceCreateTimeout)
	instance, err := w.WaitFor(ctx, func(ctx context.Context) (*dbaassdk.DBInstanceSchema, error) {
		instance, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		if instance.Status == dbaassdk.DBInstanceStatusACTIVE {
			return instance, nil
		}
		if instance.Status == dbaassdk.DBInstanceStatusERROR || instance.Status == dbaassdk.DBInstanceStatusABNORMAL {
			return nil, fmt.Errorf("status is %s: %s", instance.Status, instance.ErrorMessage)
		}
		return nil, fmt.Errorf("status is %s: %w", instance.Status, wait.ErrNotReady)
	})
	if err != nil {
		return nil, fmt.Errorf("DB instance %s: %w", id, err)
	}
	return instance, nil
}

// waitUntilDeleted polls until the instance is gone. The backend keeps the
// row with operational_status DELETING until the core call finishes, and sets
// FAILED when that call fails.
func (r *InstanceResource) waitUntilDeleted(ctx context.Context, id core.UUID) error {
	w := wait.New[struct{}](instancePollInterval, instanceDeleteTimeout)
	_, err := w.WaitFor(ctx, func(ctx context.Context) (struct{}, error) {
		instance, err := r.get(ctx, id)
		if errors.Is(err, dbaassdk.ErrNotFound) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		if instance.OperationalStatus == dbaassdk.DBInstanceOperationalStatusFAILED {
			return struct{}{}, fmt.Errorf("deletion failed: %s", instance.ErrorMessage)
		}
		return struct{}{}, fmt.Errorf("operational_status is %s: %w", instance.OperationalStatus, wait.ErrNotReady)
	})
	if err != nil {
		return fmt.Errorf("DB instance %s: %w", id, err)
	}
	return nil
}

// buildInstanceCreateBody resolves the catalog selectors in the region and
// records the resolved IDs in plan. Values that are not configured stay out of
// the request so that the backend applies its defaults.
func buildInstanceCreateBody(ctx context.Context, plan *InstanceResourceModel, rootPassword types.String, regionID core.UUID, client *dbaassdk.Client) (dbaassdk.DBInstanceCreateSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body dbaassdk.DBInstanceCreateSchema
	subnetID, subnetDiags := parse.UUIDString(reference.Get(plan.Subnet, reference.ID), "subnet.id")
	diags.Append(subnetDiags...)
	if !known(rootPassword) {
		diags.AddAttributeError(path.Root("root_password"), "Missing root password", "root_password must be configured and known.")
	}
	if diags.HasError() {
		return body, diags
	}
	databaseType := dbaassdk.DatabaseType(trimmed(plan.DatabaseType))
	version, versionDiags := dbaasselector.ResolveDBVersion(ctx, plan.DBVersion, "db_version", databaseType, regionID, dbaaslookup.NewDBVersionFinder(client))
	diags.Append(versionDiags...)
	flavor, flavorDiags := dbaasselector.ResolveFlavor(ctx, plan.Flavor, "flavor", regionID, dbaaslookup.NewFlavorFinder(client))
	diags.Append(flavorDiags...)
	volumeType, volumeTypeDiags := dbaasselector.ResolveVolumeType(ctx, plan.VolumeType, "volume_type", regionID, dbaaslookup.NewVolumeTypeFinder(client))
	diags.Append(volumeTypeDiags...)
	if diags.HasError() {
		return body, diags
	}
	plan.DBVersion = selector.WithID(plan.DBVersion, types.StringValue(version.Id.String()))
	plan.Flavor = selector.WithID(plan.Flavor, types.StringValue(flavor.Id.String()))
	plan.VolumeType = selector.WithID(plan.VolumeType, types.StringValue(volumeType.Id.String()))

	body = dbaassdk.DBInstanceCreateSchema{
		Name: trimmed(plan.Name), DatabaseType: databaseType, DbVersionId: version.Id,
		RegionId: regionID, FlavorId: flavor.Id, VolumeTypeId: volumeType.Id,
		DiskSize: int(plan.DiskSize.ValueInt64()), Solution: dbaassdk.Solution(trimmed(plan.Solution)),
		SubnetId: subnetID,
		// root_password is sent untrimmed, because whitespace is part of a password.
		RootPassword: new(rootPassword.ValueString()),
	}
	if known(plan.Description) {
		body.Description = new(trimmed(plan.Description))
	}
	if !plan.EnablePublicAccess.IsNull() && !plan.EnablePublicAccess.IsUnknown() {
		body.EnablePublicAccess = new(plan.EnablePublicAccess.ValueBool())
	}
	if !plan.BackupPolicy.IsNull() && !plan.BackupPolicy.IsUnknown() {
		policy, policyDiags := decodeBackupPolicy(ctx, plan.BackupPolicy)
		diags.Append(policyDiags...)
		if !policyDiags.HasError() {
			body.BackupPolicy, policyDiags = buildBackupPolicy(policy)
			diags.Append(policyDiags...)
		}
	}
	return body, diags
}

// buildInstanceUpdateBody sends only the fields whose planned value is known
// and differs from state. backup_policy and auto_increase_volume_size_config
// are unions, so a change to any child sends the whole planned object.
func buildInstanceUpdateBody(ctx context.Context, plan, state InstanceResourceModel) (dbaassdk.DBInstancePartialUpdateSchema, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	var body dbaassdk.DBInstancePartialUpdateSchema
	changed := false
	if known(plan.Description) && !compare.SameTrimmedString(plan.Description, state.Description) {
		body.Description, changed = new(trimmed(plan.Description)), true
	}
	if !plan.KeepBackup.IsNull() && !plan.KeepBackup.IsUnknown() && !plan.KeepBackup.Equal(state.KeepBackup) {
		body.KeepBackup, changed = new(plan.KeepBackup.ValueBool()), true
	}
	if objectChanged(plan.BackupPolicy, state.BackupPolicy) {
		policy, policyDiags := decodeBackupPolicy(ctx, plan.BackupPolicy)
		diags.Append(policyDiags...)
		if !policyDiags.HasError() {
			body.BackupPolicy, policyDiags = buildBackupPolicy(policy)
			diags.Append(policyDiags...)
			changed = true
		}
	}
	if objectChanged(plan.AutoIncreaseVolumeSize, state.AutoIncreaseVolumeSize) {
		config, configDiags := decodeAutoIncrease(ctx, plan.AutoIncreaseVolumeSize)
		diags.Append(configDiags...)
		if !configDiags.HasError() {
			body.AutoIncreaseVolumeSizeConfig, configDiags = buildAutoIncrease(config)
			diags.Append(configDiags...)
			changed = true
		}
	}
	return body, changed, diags
}

// buildBackupPolicy builds the backup_policy union from a decoded policy.
// day_of_week travels only with a weekly interval.
func buildBackupPolicy(m backupPolicyModel) (*dbaassdk.DBInstanceBackupPolicySchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	if m.Enabled.IsNull() || m.Enabled.IsUnknown() {
		diags.AddAttributeError(path.Root("backup_policy").AtName("enabled"), "Missing backup policy flag", "backup_policy.enabled must be configured and known.")
		return nil, diags
	}
	var variant any = dbaassdk.DisableAutoBackupSchema{EnableAutoBackup: false}
	if m.Enabled.ValueBool() {
		for name, value := range map[string]attrState{"backup_interval": m.BackupInterval, "start_hour": m.StartHour, "max_auto_backup": m.MaxAutoBackup} {
			if value.IsUnknown() {
				diags.AddAttributeError(path.Root("backup_policy").AtName(name), "Unknown backup policy value", "backup_policy."+name+" must be known before apply.")
			}
		}
		if diags.HasError() {
			return nil, diags
		}
		enabled := dbaassdk.EnableAutoBackupSchema{
			EnableAutoBackup: true, BackupInterval: dbaassdk.BackupInterval(trimmed(m.BackupInterval)),
			StartHour: int(m.StartHour.ValueInt64()), MaxAutoBackup: int(m.MaxAutoBackup.ValueInt64()),
		}
		if enabled.BackupInterval == dbaassdk.BackupIntervalWeekly {
			if !known(m.DayOfWeek) {
				diags.AddAttributeError(path.Root("backup_policy").AtName("day_of_week"), "Missing backup day", "backup_policy.day_of_week must be known for a weekly interval.")
				return nil, diags
			}
			enabled.DayOfWeek = core.NullableFromPtr(new(dbaassdk.DayOfWeek(trimmed(m.DayOfWeek))))
		}
		variant = enabled
	}
	var union dbaassdk.DBInstanceBackupPolicySchema
	if err := setUnion(&union, variant); err != nil {
		diags.AddError("Unable to build backup policy", err.Error())
		return nil, diags
	}
	return &union, diags
}

// buildAutoIncrease builds the auto_increase_volume_size_config union.
func buildAutoIncrease(m autoIncreaseModel) (*dbaassdk.UpdateAutoIncreaseVolumeSizeConfigSchema, diag.Diagnostics) {
	var diags diag.Diagnostics
	if m.Enabled.IsNull() || m.Enabled.IsUnknown() {
		diags.AddAttributeError(path.Root("auto_increase_volume_size").AtName("enabled"), "Missing auto-increase flag", "auto_increase_volume_size.enabled must be configured and known.")
		return nil, diags
	}
	var variant any = dbaassdk.DisableAutoIncreaseVolumeSizeSchema{EnableAutoIncreaseVolumeSize: false}
	if m.Enabled.ValueBool() {
		if m.Threshold.IsUnknown() || m.Limit.IsUnknown() {
			diags.AddAttributeError(path.Root("auto_increase_volume_size"), "Unknown auto-increase value", "auto_increase_volume_size.threshold and limit must be known before apply.")
			return nil, diags
		}
		variant = dbaassdk.EnableAutoIncreaseVolumeSizeSchema{
			EnableAutoIncreaseVolumeSize: true, Threshold: int(m.Threshold.ValueInt64()), AutoIncreaseLimit: int(m.Limit.ValueInt64()),
		}
	}
	var union dbaassdk.UpdateAutoIncreaseVolumeSizeConfigSchema
	if err := setUnion(&union, variant); err != nil {
		diags.AddError("Unable to build auto-increase configuration", err.Error())
		return nil, diags
	}
	return &union, diags
}

// setUnion stores variant in an SDK union. The SDK From* helpers overwrite the
// discriminator with the strings "True" and "False", while the API declares a
// boolean, so the variant is marshaled as it is.
func setUnion(union json.Unmarshaler, variant any) error {
	raw, err := json.Marshal(variant)
	if err != nil {
		return err
	}
	return union.UnmarshalJSON(raw)
}

// planDayOfWeek leaves backup_policy.day_of_week unknown when the interval
// changes and configuration omits the day. The request carries no day for a
// daily interval, and the backend then clears it.
func planDayOfWeek(ctx context.Context, resp *resource.ModifyPlanResponse, config, plan, state InstanceResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if config.BackupPolicy.IsNull() || config.BackupPolicy.IsUnknown() || plan.BackupPolicy.IsNull() || plan.BackupPolicy.IsUnknown() || state.BackupPolicy.IsNull() {
		return diags
	}
	configured, configDiags := decodeBackupPolicy(ctx, config.BackupPolicy)
	diags.Append(configDiags...)
	planned, planDiags := decodeBackupPolicy(ctx, plan.BackupPolicy)
	diags.Append(planDiags...)
	prior, stateDiags := decodeBackupPolicy(ctx, state.BackupPolicy)
	diags.Append(stateDiags...)
	sameInterval := planned.BackupInterval.Equal(prior.BackupInterval) || compare.SameTrimmedString(planned.BackupInterval, prior.BackupInterval)
	if diags.HasError() || !configured.DayOfWeek.IsNull() || sameInterval {
		return diags
	}
	diags.Append(resp.Plan.SetAttribute(ctx, path.Root("backup_policy").AtName("day_of_week"), types.StringUnknown())...)
	return diags
}

// instanceImmutableChanged mirrors the replace plan modifiers, which
// ModifyPlan cannot observe, with the same comparisons.
func instanceImmutableChanged(plan, state, config InstanceResourceModel) bool {
	changed := !compare.SameTrimmedString(plan.Name, state.Name) || !compare.SameTrimmedString(plan.DatabaseType, state.DatabaseType) ||
		!compare.SameTrimmedString(plan.Solution, state.Solution) || !plan.DiskSize.Equal(state.DiskSize) || !sameRegion(plan.Region, state.Region) ||
		!compare.SameUUIDString(reference.Get(plan.Subnet, reference.ID), reference.Get(state.Subnet, reference.ID))
	return changed || !config.EnablePublicAccess.IsNull() && !plan.EnablePublicAccess.Equal(state.EnablePublicAccess)
}

// validateCreateDefaults rejects values that a new instance cannot have. The
// create request has no keep_backup or auto-increase field, so the backend
// starts every instance with keep_backup = true and auto-increase disabled.
func validateCreateDefaults(ctx context.Context, config InstanceResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	const detail = " Create the instance with the default first, then change it in a later apply."
	if !config.KeepBackup.IsNull() && !config.KeepBackup.IsUnknown() && !config.KeepBackup.ValueBool() {
		diags.AddAttributeError(path.Root("keep_backup"), "Unsupported value at creation", "A new DB instance always has keep_backup = true."+detail)
	}
	if !config.AutoIncreaseVolumeSize.IsNull() && !config.AutoIncreaseVolumeSize.IsUnknown() {
		m, decodeDiags := decodeAutoIncrease(ctx, config.AutoIncreaseVolumeSize)
		diags.Append(decodeDiags...)
		if !decodeDiags.HasError() && !m.Enabled.IsNull() && !m.Enabled.IsUnknown() && m.Enabled.ValueBool() {
			diags.AddAttributeError(path.Root("auto_increase_volume_size").AtName("enabled"), "Unsupported value at creation", "A new DB instance always has auto_increase_volume_size disabled."+detail)
		}
	}
	return diags
}

// validateBackupPolicyConfig checks the union rules of backup_policy. The
// disabled variant carries only the flag, so a configured child would plan a
// value that the backend never writes.
func validateBackupPolicyConfig(ctx context.Context, value types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	m, decodeDiags := decodeBackupPolicy(ctx, value)
	if decodeDiags.HasError() {
		return decodeDiags
	}
	root := path.Root("backup_policy")
	if m.Enabled.IsNull() {
		diags.AddAttributeError(root.AtName("enabled"), "Missing backup policy flag", "backup_policy.enabled is required when backup_policy is configured.")
		return diags
	}
	if m.Enabled.IsUnknown() {
		return diags
	}
	children := map[string]attrState{"backup_interval": m.BackupInterval, "day_of_week": m.DayOfWeek, "start_hour": m.StartHour, "max_auto_backup": m.MaxAutoBackup}
	if !m.Enabled.ValueBool() {
		for name, child := range children {
			if !child.IsNull() {
				diags.AddAttributeError(root.AtName(name), "Invalid backup policy", "backup_policy."+name+" must be omitted when enabled is false.")
			}
		}
		return diags
	}
	for _, name := range []string{"backup_interval", "start_hour", "max_auto_backup"} {
		if children[name].IsNull() {
			diags.AddAttributeError(root.AtName(name), "Invalid backup policy", "backup_policy."+name+" is required when enabled is true.")
		}
	}
	diags.Append(validateRange(m.StartHour, root.AtName("start_hour"), 0, 23, 1)...)
	diags.Append(validateRange(m.MaxAutoBackup, root.AtName("max_auto_backup"), 7, 180, 1)...)
	if !known(m.BackupInterval) {
		return diags
	}
	switch interval := dbaassdk.BackupInterval(trimmed(m.BackupInterval)); interval {
	case dbaassdk.BackupIntervalWeekly:
		if m.DayOfWeek.IsNull() {
			diags.AddAttributeError(root.AtName("day_of_week"), "Invalid backup policy", "backup_policy.day_of_week is required when backup_interval is weekly.")
		} else if known(m.DayOfWeek) && !dbaassdk.DayOfWeek(trimmed(m.DayOfWeek)).Valid() {
			diags.AddAttributeError(root.AtName("day_of_week"), "Invalid backup policy", "backup_policy.day_of_week must be a lowercase day name, such as monday.")
		}
	case dbaassdk.BackupIntervalDaily:
		if !m.DayOfWeek.IsNull() {
			diags.AddAttributeError(root.AtName("day_of_week"), "Invalid backup policy", "backup_policy.day_of_week must be omitted when backup_interval is daily.")
		}
	default:
		diags.AddAttributeError(root.AtName("backup_interval"), "Invalid backup policy", "backup_policy.backup_interval must be daily or weekly.")
	}
	return diags
}

// validateAutoIncreaseConfig checks the union rules and ranges of
// auto_increase_volume_size.
func validateAutoIncreaseConfig(ctx context.Context, value types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	m, decodeDiags := decodeAutoIncrease(ctx, value)
	if decodeDiags.HasError() {
		return decodeDiags
	}
	root := path.Root("auto_increase_volume_size")
	if m.Enabled.IsNull() {
		diags.AddAttributeError(root.AtName("enabled"), "Missing auto-increase flag", "auto_increase_volume_size.enabled is required when auto_increase_volume_size is configured.")
		return diags
	}
	if m.Enabled.IsUnknown() {
		return diags
	}
	children := map[string]attrState{"threshold": m.Threshold, "limit": m.Limit}
	for name, child := range children {
		if m.Enabled.ValueBool() && child.IsNull() {
			diags.AddAttributeError(root.AtName(name), "Invalid auto-increase configuration", "auto_increase_volume_size."+name+" is required when enabled is true.")
		}
		if !m.Enabled.ValueBool() && !child.IsNull() {
			diags.AddAttributeError(root.AtName(name), "Invalid auto-increase configuration", "auto_increase_volume_size."+name+" must be omitted when enabled is false.")
		}
	}
	if m.Enabled.ValueBool() {
		diags.Append(validateRange(m.Threshold, root.AtName("threshold"), 50, 95, 1)...)
		diags.Append(validateRange(m.Limit, root.AtName("limit"), 20, 3000, 10)...)
	}
	return diags
}

// attrState is the part of a Framework value that presence checks read.
type attrState interface {
	IsNull() bool
	IsUnknown() bool
}

// objectChanged reports whether a known planned child of an object differs
// from state. An unknown child is not a change by itself, and neither is a
// string child that differs only by outer whitespace.
func objectChanged(plan, state types.Object) bool {
	if plan.IsNull() || plan.IsUnknown() {
		return false
	}
	if state.IsNull() || state.IsUnknown() {
		return true
	}
	for name, value := range plan.Attributes() {
		prior := state.Attributes()[name]
		if text, ok := value.(types.String); ok {
			if priorText, ok := prior.(types.String); ok && compare.SameTrimmedString(text, priorText) {
				continue
			}
		}
		if !value.IsUnknown() && !value.Equal(prior) {
			return true
		}
	}
	return false
}

func validateRange(value types.Int64, p path.Path, minimum, maximum, step int64) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	if v := value.ValueInt64(); v < minimum || v > maximum || v%step != 0 {
		detail := fmt.Sprintf("%s must be between %d and %d", p, minimum, maximum)
		if step > 1 {
			detail += fmt.Sprintf(" and a multiple of %d", step)
		}
		diags.AddAttributeError(p, "Value out of range", detail+".")
	}
	return diags
}

// validateNotBlank rejects a known value that is empty after the trim,
// because the API requires a non-empty value.
func validateNotBlank(value types.String, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	if known(value) && trimmed(value) == "" {
		diags.AddAttributeError(p, "Invalid value", p.String()+" must not be empty or whitespace-only.")
	}
	return diags
}

func known(value types.String) bool { return !value.IsNull() && !value.IsUnknown() }

// trimmed returns a user-supplied value without outer whitespace, which the
// API removes from request strings.
func trimmed(value types.String) string { return strings.TrimSpace(value.ValueString()) }

// sameRegion reports whether two region references name the same region, as
// the region plan modifier compares them.
func sameRegion(plan, state types.Object) bool {
	return compare.SameTrimmedString(reference.Get(plan, reference.Name), reference.Get(state, reference.Name))
}

func collectIDs[T any](items []T, id func(T) core.UUID) []core.UUID {
	ids := make([]core.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, id(item))
	}
	return ids
}
