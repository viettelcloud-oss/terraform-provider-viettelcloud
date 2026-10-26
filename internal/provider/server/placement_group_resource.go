package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                = &PlacementGroupResource{}
	_ resource.ResourceWithImportState = &PlacementGroupResource{}
)

type PlacementGroupResource struct {
	client    *serversdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewPlacementGroupResource() resource.Resource {
	return &PlacementGroupResource{}
}

func (r *PlacementGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_placement_group"
}

func (r *PlacementGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Placement Group.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Placement Group ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the Placement Group.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description of the Placement Group. Omitting it preserves the backend value; set it to an empty string to clear it.",
			},
			"policy": schema.StringAttribute{
				Required:    true,
				Description: "Placement Group policy: one of affinity, anti-affinity, soft-affinity, or soft-anti-affinity. Changing the policy replaces the Placement Group.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"region": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Project region where the Placement Group is created. Changing the region replaces the Placement Group.",
				Attributes:  reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"region_id": schema.StringAttribute{
				Computed:    true,
				Description: "Region ID (UUID) backing the configured region.",
			},
			"project": schema.StringAttribute{
				Computed:    true,
				Description: "Project name owning the Placement Group.",
			},
			"server_count": schema.Int64Attribute{
				Computed:    true,
				Description: "Number of servers currently assigned to the Placement Group.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Placement Group was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Placement Group was last updated (RFC3339).",
			},
		},
	}
}

func (r *PlacementGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.Server
	r.project = data.Project
	r.projectID = data.ProjectID
}

func (r *PlacementGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PlacementGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildPlacementGroupCreateBody(ctx, plan, projectlookup.NewRegionFinder(r.project, r.projectID).Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreatePlacementGroup(ctx, serversdk.CreatePlacementGroupParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Placement Group", err.Error())
		return
	}

	populatePlacementGroupResourceState(created, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PlacementGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PlacementGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pgID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	pg, err := r.get(ctx, pgID)
	if err != nil {
		if errors.Is(err, serversdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Placement Group", err.Error())
		return
	}

	populatePlacementGroupResourceState(pg, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PlacementGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PlacementGroupResourceModel
	var state PlacementGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pgID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, changed, diags := buildPlacementGroupUpdateBody(plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var pg *serversdk.PlacementGroupSchema
	var err error
	if changed {
		pg, err = r.client.PartialUpdatePlacementGroup(ctx, pgID, serversdk.PartialUpdatePlacementGroupParams{
			ProjectID: r.projectID,
		}, body)
	} else {
		pg, err = r.get(ctx, pgID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating Placement Group", err.Error())
		return
	}
	if pg.Id != pgID {
		resp.Diagnostics.AddError(
			"Error updating Placement Group",
			fmt.Sprintf("The API answered the update of Placement Group %s with Placement Group %s, so the update was not applied in place.", pgID, pg.Id),
		)
		return
	}

	newState := plan
	populatePlacementGroupResourceState(pg, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *PlacementGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PlacementGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pgID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeletePlacementGroup(ctx, pgID, serversdk.DeletePlacementGroupParams{
		ProjectID: r.projectID,
	})
	if err != nil && !errors.Is(err, serversdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting Placement Group", err.Error())
	}
}

func (r *PlacementGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *PlacementGroupResource) get(ctx context.Context, id core.UUID) (*serversdk.PlacementGroupSchema, error) {
	return r.client.GetPlacementGroup(ctx, id, serversdk.GetPlacementGroupParams{ProjectID: r.projectID})
}

func buildPlacementGroupCreateBody(
	ctx context.Context,
	plan PlacementGroupResourceModel,
	resolveRegion projectlookup.RegionResolveFunc,
) (serversdk.PlacementGroupCreateSchema, diag.Diagnostics) {
	var body serversdk.PlacementGroupCreateSchema
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		diags.AddError("Missing Placement Group name", "name must be configured and known.")
	}
	if plan.Policy.IsNull() || plan.Policy.IsUnknown() || strings.TrimSpace(plan.Policy.ValueString()) == "" {
		diags.AddError("Missing Placement Group policy", "policy must be configured and known.")
	}
	planRegion := reference.Get(plan.Region, reference.Name)
	if planRegion.IsNull() || planRegion.IsUnknown() || strings.TrimSpace(planRegion.ValueString()) == "" {
		diags.AddError("Missing Placement Group region", "region.name must be configured and known.")
	}
	if diags.HasError() {
		return body, diags
	}

	policyValue := serversdk.PlacementGroupPolicy(plan.Policy.ValueString())
	if !policyValue.Valid() {
		diags.AddError(
			"Invalid Placement Group policy",
			fmt.Sprintf("policy %q is not one of affinity, anti-affinity, soft-affinity, soft-anti-affinity.", plan.Policy.ValueString()),
		)
		return body, diags
	}

	name := strings.TrimSpace(plan.Name.ValueString())
	regionName := strings.TrimSpace(planRegion.ValueString())
	region, err := resolveRegion(ctx, projectlookup.RegionFilter{Name: &regionName})
	if err != nil {
		diags.AddError("Unable to resolve region", "region.name: "+err.Error())
		return body, diags
	}

	body.Name = name
	body.Policy = policyValue
	body.RegionId = region.Region.Id
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	return body, diags
}

// buildPlacementGroupUpdateBody sends only the fields that changed since state.
// The backend's PATCH endpoint does not accept policy or region_id, so a
// policy/region change is enforced as a replacement at the schema level.
func buildPlacementGroupUpdateBody(
	plan PlacementGroupResourceModel,
	state PlacementGroupResourceModel,
) (serversdk.PlacementGroupPartialUpdateSchema, bool, diag.Diagnostics) {
	var body serversdk.PlacementGroupPartialUpdateSchema
	var diags diag.Diagnostics
	changed := false

	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		diags.AddError("Missing Placement Group name", "name must be configured and known.")
		return body, changed, diags
	}
	desiredName := strings.TrimSpace(plan.Name.ValueString())
	if state.Name.IsNull() || state.Name.IsUnknown() || strings.TrimSpace(state.Name.ValueString()) != desiredName {
		body.Name = &desiredName
		changed = true
	}

	if !plan.Description.IsNull() && !plan.Description.IsUnknown() &&
		(state.Description.IsNull() || state.Description.IsUnknown() || !plan.Description.Equal(state.Description)) {
		value := plan.Description.ValueString()
		body.Description = &value
		changed = true
	}
	return body, changed, diags
}
