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
	_ resource.Resource                = &VPCResource{}
	_ resource.ResourceWithImportState = &VPCResource{}
)

type VPCResource struct {
	client    *networksdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewVPCResource() resource.Resource {
	return &VPCResource{}
}

func (r *VPCResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpc"
}

func (r *VPCResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud VPC.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "VPC ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the VPC.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description of the VPC.",
			},
			"cidr": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Description: "IPv4 network CIDR, e.g. 10.0.0.0/16.",
			},
			"region": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Project region where the VPC is created.",
				Attributes:  reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"display_name": schema.StringAttribute{
				Computed:    true,
				Description: "Display name assigned by the platform.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the VPC was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the VPC was last updated (RFC3339).",
			},
		},
	}
}

func (r *VPCResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VPCResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VPCResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	regionFinder := projectlookup.NewRegionFinder(r.project, r.projectID)
	body, diags := buildVPCCreateBody(ctx, plan, regionFinder.Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	vpc, err := r.client.CreateVpc(ctx, networksdk.CreateVpcParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating VPC", err.Error())
		return
	}

	populateVPCResourceState(vpc, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *VPCResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VPCResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vpcID, diag := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	vpc, err := r.client.GetVpc(ctx, vpcID, networksdk.GetVpcParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading VPC", err.Error())
		return
	}

	populateVPCResourceState(vpc, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *VPCResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan VPCResourceModel
	var state VPCResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vpcID, diag := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := buildVPCUpdateBody(plan)

	vpc, err := r.client.UpdateVpc(ctx, vpcID, networksdk.UpdateVpcParams{
		ProjectID: r.projectID,
	}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating VPC", err.Error())
		return
	}

	newState := plan
	populateVPCResourceState(vpc, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *VPCResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VPCResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vpcID, diag := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diag...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteVpc(ctx, vpcID, networksdk.DeleteVpcParams{
		ProjectID: r.projectID,
	})
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Error deleting VPC", err.Error())
		return
	}
}

func (r *VPCResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildVPCCreateBody(
	ctx context.Context,
	plan VPCResourceModel,
	resolveRegion projectlookup.RegionResolveFunc,
) (networksdk.VPCCreateSchema, diag.Diagnostics) {
	var body networksdk.VPCCreateSchema
	regionID, diags := resolveRegionReference(ctx, plan.Region, resolveRegion)
	if diags.HasError() {
		return body, diags
	}

	body.Name = strings.TrimSpace(plan.Name.ValueString())
	body.Cidr = plan.CIDR.ValueString()
	body.RegionId = regionID
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := plan.Description.ValueString()
		body.Description = &description
	}
	return body, diags
}

func buildVPCUpdateBody(plan VPCResourceModel) networksdk.VPCUpdateSchema {
	body := networksdk.VPCUpdateSchema{Name: strings.TrimSpace(plan.Name.ValueString())}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description := plan.Description.ValueString()
		body.Description = &description
	}
	return body
}
