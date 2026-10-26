package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                = &SecurityGroupResource{}
	_ resource.ResourceWithImportState = &SecurityGroupResource{}
)

type SecurityGroupResource struct {
	client    *networksdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewSecurityGroupResource() resource.Resource {
	return &SecurityGroupResource{}
}

func (r *SecurityGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group"
}

func (r *SecurityGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud security group.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Security group ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the security group.",
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the backend.",
			},
			"description": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Optional description of the security group. Omitting it preserves the backend value; " +
					"set it to an empty string to clear it. A security group created without a description reports " +
					"an empty string rather than a null value.",
			},
			"is_default": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether this is the project's default security group.",
			},
			"region": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Project region where the security group is created. Changing it replaces the security group because the update operation cannot move one between regions.",
				Attributes:  reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"region_id": schema.StringAttribute{
				Computed:    true,
				Description: "Region ID (UUID) that owns the security group.",
			},
			"project_id": schema.StringAttribute{
				Computed:    true,
				Description: "Project ID (UUID) that owns the security group.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the security group was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the security group was last updated (RFC3339).",
			},
		},
	}
}

func (r *SecurityGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.Network
	r.project = data.Project
	r.projectID = data.ProjectID
}

func (r *SecurityGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SecurityGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	regionFinder := projectlookup.NewRegionFinder(r.project, r.projectID)
	body, diags := buildSecurityGroupCreateBody(ctx, plan, regionFinder.Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	sg, err := r.client.CreateSecurityGroup(ctx, networksdk.CreateSecurityGroupParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating security group", err.Error())
		return
	}

	populateSecurityGroupResourceState(sg, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SecurityGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SecurityGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sgID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	sg, err := r.get(ctx, sgID)
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading security group", err.Error())
		return
	}

	populateSecurityGroupResourceState(sg, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SecurityGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SecurityGroupResourceModel
	var state SecurityGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sgID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var sg *networksdk.SecurityGroupSchema
	var err error
	if body, changed := buildSecurityGroupUpdateBody(plan, state); changed {
		sg, err = r.client.PartialUpdateSecurityGroup(ctx, sgID, networksdk.PartialUpdateSecurityGroupParams{ProjectID: r.projectID}, body)
		if err != nil {
			resp.Diagnostics.AddError("Error updating security group", err.Error())
			return
		}
	} else {
		// Read rather than send an empty patch: every mutable field already
		// matches the backend, so the plan is carried by a computed value.
		sg, err = r.get(ctx, sgID)
		if err != nil {
			resp.Diagnostics.AddError("Error reading security group after update", err.Error())
			return
		}
	}

	newState := plan
	populateSecurityGroupResourceState(sg, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *SecurityGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SecurityGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sgID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSecurityGroup(ctx, sgID, networksdk.DeleteSecurityGroupParams{ProjectID: r.projectID})
	if err != nil && !errors.Is(err, networksdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting security group", err.Error())
	}
}

func (r *SecurityGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *SecurityGroupResource) get(ctx context.Context, id core.UUID) (*networksdk.SecurityGroupSchema, error) {
	return r.client.GetSecurityGroup(ctx, id, networksdk.GetSecurityGroupParams{ProjectID: r.projectID})
}

func buildSecurityGroupCreateBody(
	ctx context.Context,
	plan SecurityGroupResourceModel,
	resolveRegion projectlookup.RegionResolveFunc,
) (networksdk.SecurityGroupCreateSchema, diag.Diagnostics) {
	var body networksdk.SecurityGroupCreateSchema
	var diags diag.Diagnostics

	if plan.Name.IsNull() || plan.Name.IsUnknown() || strings.TrimSpace(plan.Name.ValueString()) == "" {
		diags.AddError("Missing name", "name must be configured and known.")
		return body, diags
	}
	regionID, regionDiags := resolveRegionReference(ctx, plan.Region, resolveRegion)
	diags.Append(regionDiags...)
	if diags.HasError() {
		return body, diags
	}

	body.Name = plan.Name.ValueString()
	body.RegionId = regionID
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		body.Description = &value
	}

	return body, diags
}

// The patch preserves every field the body omits, so only changed fields are
// sent. An unknown description belongs to a practitioner who left it
// unconfigured, so the body omits it rather than guess. A known empty string is
// instead a request to clear the description.
func buildSecurityGroupUpdateBody(plan, state SecurityGroupResourceModel) (networksdk.SecurityGroupPartialUpdateSchema, bool) {
	var body networksdk.SecurityGroupPartialUpdateSchema
	changed := false

	if !plan.Name.IsNull() && !plan.Name.IsUnknown() {
		// The backend trims the name, and state may hold the practitioner's
		// untrimmed representation of the same name. Compare what the backend
		// would store rather than the two representations.
		if name := strings.TrimSpace(plan.Name.ValueString()); name != strings.TrimSpace(state.Name.ValueString()) {
			body.Name = &name
			changed = true
		}
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		description := plan.Description.ValueString()
		body.Description = &description
		changed = true
	}

	return body, changed
}
