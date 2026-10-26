package network

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	projectlookup "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                = &ElasticIPResource{}
	_ resource.ResourceWithImportState = &ElasticIPResource{}
)

const (
	elasticIPReadyTimeout      = 5 * time.Minute
	elasticIPReadyPollInterval = 3 * time.Second
)

type ElasticIPResource struct {
	client    *networksdk.Client
	project   *projectsdk.Client
	projectID core.UUID
}

func NewElasticIPResource() resource.Resource {
	return &ElasticIPResource{}
}

func (r *ElasticIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_elastic_ip"
}

func (r *ElasticIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a Viettel Cloud Elastic IP.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Elastic IP ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"region": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Project region where the Elastic IP is created.",
				Attributes:  reference.Attributes(reference.Name, "Region name."),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional description of the Elastic IP. Omitting it preserves the backend value; set it to an empty string to clear it.",
			},
			"enable_ipv4": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Enable IPv4. Defaults to true. Changing a configured value replaces the Elastic IP.",
				PlanModifiers: []planmodifier.Bool{
					// UseStateForUnknown must precede the replacement modifier. Any
					// planned change marks every Computed attribute that is null in
					// configuration as unknown, and an unresolved unknown differs from
					// state, which would force replacement on unrelated updates.
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"enable_ipv6": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Enable IPv6. Defaults to false. Changing a configured value replaces the Elastic IP.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
					boolplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"ip_address": schema.StringAttribute{
				Computed:    true,
				Description: "The assigned IPv4 address.",
			},
			"ipv6_address": schema.StringAttribute{
				Computed:    true,
				Description: "The assigned IPv6 address.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current Elastic IP status.",
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Elastic IP was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Elastic IP was last updated (RFC3339).",
			},
		},
	}
}

func (r *ElasticIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ElasticIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ElasticIPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	regionFinder := projectlookup.NewRegionFinder(r.project, r.projectID)
	body, diags := buildElasticIPCreateBody(ctx, plan, regionFinder.Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createdEIP, err := r.client.CreateElasticIp(ctx, networksdk.CreateElasticIpParams{ProjectID: r.projectID}, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Elastic IP", err.Error())
		return
	}

	eipID := createdEIP.Id
	populateElasticIPPendingState(&plan, eipID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	eip, err := r.waitUntilReady(
		ctx,
		eipID,
		createdEIP.EnableIpv4,
		createdEIP.EnableIpv6,
		elasticIPReadyTimeout,
		elasticIPReadyPollInterval,
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error waiting for Elastic IP to be ready",
			fmt.Sprintf("%s\n\nElastic IP ID %s has been preserved in Terraform state and will be marked tainted so a retry does not create an untracked duplicate.", err, eipID),
		)
		return
	}

	populateElasticIPResourceState(eip, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ElasticIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ElasticIPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	eipID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	eip, err := r.get(ctx, eipID)
	if err != nil {
		if errors.Is(err, networksdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Elastic IP", err.Error())
		return
	}

	populateElasticIPResourceState(eip, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ElasticIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ElasticIPResourceModel
	var state ElasticIPResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	eipID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var eip *networksdk.ElasticIPDetailSchema
	var err error
	if body, changed := buildElasticIPUpdateBody(plan, state); changed {
		eip, err = r.client.PartialUpdateElasticIp(ctx, eipID, networksdk.PartialUpdateElasticIpParams{
			ProjectID: r.projectID,
		}, body)
	} else {
		eip, err = r.get(ctx, eipID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating Elastic IP", err.Error())
		return
	}
	if eip.Id != eipID {
		resp.Diagnostics.AddError(
			"Error updating Elastic IP",
			fmt.Sprintf("The API answered the update of Elastic IP %s with Elastic IP %s, so the update was not applied in place.", eipID, eip.Id),
		)
		return
	}

	newState := plan
	populateElasticIPResourceState(eip, &newState)
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

func (r *ElasticIPResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ElasticIPResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	eipID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteElasticIp(ctx, eipID, networksdk.DeleteElasticIpParams{ProjectID: r.projectID})
	if err != nil && !errors.Is(err, networksdk.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting Elastic IP", err.Error())
	}
}

func (r *ElasticIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *ElasticIPResource) get(ctx context.Context, id core.UUID) (*networksdk.ElasticIPDetailSchema, error) {
	return r.client.GetElasticIp(ctx, id, networksdk.GetElasticIpParams{ProjectID: r.projectID})
}

func (r *ElasticIPResource) waitUntilReady(
	ctx context.Context,
	id core.UUID,
	ipv4 bool,
	ipv6 bool,
	timeout time.Duration,
	pollInterval time.Duration,
) (*networksdk.ElasticIPDetailSchema, error) {
	w := wait.New[*networksdk.ElasticIPDetailSchema](pollInterval, timeout)

	eip, err := w.WaitFor(ctx, func(ctx context.Context) (*networksdk.ElasticIPDetailSchema, error) {
		eip, err := r.get(ctx, id)
		if err != nil {
			return nil, err
		}
		if eip == nil {
			return nil, fmt.Errorf("not found yet: %w", wait.ErrNotReady)
		}
		if ipv4 && (eip.IpAddress == nil || *eip.IpAddress == "") {
			return nil, fmt.Errorf("no IPv4 assigned yet: %w", wait.ErrNotReady)
		}
		if ipv6 && (eip.Ipv6Address == nil || *eip.Ipv6Address == "") {
			return nil, fmt.Errorf("no IPv6 assigned yet: %w", wait.ErrNotReady)
		}
		return eip, nil
	})
	if err != nil {
		return nil, fmt.Errorf("elastic IP %s: %w", id, err)
	}
	return eip, nil
}

// buildElasticIPUpdateBody sends description only when the plan changes it.
// An unconfigured description keeps the backend value, matching create.
func buildElasticIPUpdateBody(
	plan ElasticIPResourceModel,
	state ElasticIPResourceModel,
) (networksdk.ElasticIPPartialUpdateSchema, bool) {
	var body networksdk.ElasticIPPartialUpdateSchema

	if plan.Description.IsNull() || plan.Description.IsUnknown() || plan.Description.Equal(state.Description) {
		return body, false
	}

	description := plan.Description.ValueString()
	body.Description = &description
	return body, true
}

func populateElasticIPPendingState(state *ElasticIPResourceModel, id core.UUID) {
	state.ID = types.StringValue(id.String())
	if state.Description.IsUnknown() {
		state.Description = types.StringNull()
	}
	if state.EnableIPv4.IsUnknown() {
		state.EnableIPv4 = types.BoolNull()
	}
	if state.EnableIPv6.IsUnknown() {
		state.EnableIPv6 = types.BoolNull()
	}
	state.IPAddress = types.StringNull()
	state.IPv6Address = types.StringNull()
	state.Status = types.StringNull()
	state.CreatedAt = types.StringNull()
	state.UpdatedAt = types.StringNull()
}

func buildElasticIPCreateBody(
	ctx context.Context,
	plan ElasticIPResourceModel,
	resolveRegion projectlookup.RegionResolveFunc,
) (networksdk.ElasticIPCreateSchema, diag.Diagnostics) {
	var body networksdk.ElasticIPCreateSchema
	regionID, diags := resolveRegionReference(ctx, plan.Region, resolveRegion)
	if diags.HasError() {
		return body, diags
	}

	body.RegionId = regionID
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		value := plan.Description.ValueString()
		body.Description = &value
	}
	if !plan.EnableIPv4.IsNull() && !plan.EnableIPv4.IsUnknown() {
		value := plan.EnableIPv4.ValueBool()
		body.EnableIpv4 = &value
	}
	if !plan.EnableIPv6.IsNull() && !plan.EnableIPv6.IsUnknown() {
		value := plan.EnableIPv6.ValueBool()
		body.EnableIpv6 = &value
	}

	return body, diags
}
