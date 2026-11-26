package project

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/parse"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	_ resource.Resource                   = &CustomRoleResource{}
	_ resource.ResourceWithImportState    = &CustomRoleResource{}
	_ resource.ResourceWithValidateConfig = &CustomRoleResource{}
)

// customRoleTextMaxLength is the backend limit, in characters, of a custom
// role display_name and description.
const customRoleTextMaxLength = 255

type CustomRoleResource struct {
	client    *projectsdk.Client
	projectID core.UUID
}

func NewCustomRoleResource() resource.Resource {
	return &CustomRoleResource{}
}

func (r *CustomRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_custom_role"
}

func (r *CustomRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a custom role of the configured Viettel Cloud project. A custom role is a set of IAM actions that can be assigned to project members.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Custom Role ID (UUID).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Computed:    true,
				Description: "Custom Role name, generated once at creation as a slug of display_name, for example `doi-van-hanh` for `Đội vận hành`. Changing display_name later does not change it.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"display_name": schema.StringAttribute{
				Required:    true,
				Description: "Display name of the Custom Role. Outer whitespace is ignored; the rest must be 1 to 255 characters and contain at least one letter or digit. Its slug must be unique among the project's custom roles. Changing it updates the role in place.",
				Validators: []validator.String{
					customRoleText{maxLength: customRoleTextMaxLength, notBlank: true},
				},
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Description of the Custom Role. Outer whitespace is ignored; the rest must be at most 255 characters. Omitting it preserves the current value; set it to an empty string to clear it.",
				Validators: []validator.String{
					customRoleText{maxLength: customRoleTextMaxLength},
				},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether the Custom Role grants its actions. A disabled role grants nothing. Defaults to `true`, so removing the attribute enables the role again.",
			},
			"actions": schema.SetNestedAttribute{
				Required:    true,
				Description: "IAM actions that the Custom Role grants, each referenced by its URN. Each action must exist and be allowed on projects. Set it to an empty set to grant no action.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: customRoleActionAttributes(),
				},
			},
			"urn": schema.StringAttribute{
				Computed:    true,
				Description: "Custom Role URN, `urn:role:prj-<project slug>:<name>`. It is generated at creation and never changes.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Custom Role was created (RFC3339).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:    true,
				Description: "Timestamp when the Custom Role was last updated (RFC3339).",
			},
		},
	}
}

func (r *CustomRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerdata.Configured)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *providerdata.Configured, got %T", req.ProviderData))
		return
	}
	r.client = data.Project
	r.projectID = data.ProjectID
}

func (r *CustomRoleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var actions types.Set
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("actions"), &actions)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateCustomRoleActions(actions)...)
}

func (r *CustomRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config CustomRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildCustomRoleCreateBody(plan, config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateCustomRole(ctx, r.projectID, body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Custom Role", customRoleAPIErrorDetail(err))
		return
	}

	populateCustomRoleResourceState(created, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CustomRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state CustomRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roleID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	role, err := r.get(ctx, roleID)
	if err != nil {
		if errors.Is(err, projectsdk.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Custom Role", customRoleAPIErrorDetail(err))
		return
	}

	populateCustomRoleResourceState(role, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *CustomRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state CustomRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roleID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, changed, diags := buildCustomRoleUpdateBody(plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var role *projectsdk.CustomRoleDetailSchema
	var err error
	if changed {
		role, err = r.client.UpdateCustomRole(ctx, r.projectID, roleID, body)
	} else {
		role, err = r.get(ctx, roleID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating Custom Role", customRoleAPIErrorDetail(err))
		return
	}
	if role.Id != roleID {
		resp.Diagnostics.AddError(
			"Error updating Custom Role",
			fmt.Sprintf("The API answered the update of Custom Role %s with Custom Role %s, so the update was not applied in place.", roleID, role.Id),
		)
		return
	}

	populateCustomRoleResourceState(role, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *CustomRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state CustomRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roleID, diags := parse.UUIDString(state.ID, "id")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteCustomRole(ctx, r.projectID, roleID)
	switch {
	case err == nil, errors.Is(err, projectsdk.ErrNotFound):
		return
	case errors.Is(err, projectsdk.ErrConflict):
		resp.Diagnostics.AddError(
			"Custom Role is in use",
			fmt.Sprintf("Custom Role %s is still assigned to users or groups, so it was not deleted. Remove those assignments, then destroy it again. API response: %s", roleID, customRoleAPIErrorDetail(err)),
		)
	default:
		resp.Diagnostics.AddError("Error deleting Custom Role", customRoleAPIErrorDetail(err))
	}
}

func (r *CustomRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *CustomRoleResource) get(ctx context.Context, id core.UUID) (*projectsdk.CustomRoleDetailSchema, error) {
	return r.client.GetCustomRole(ctx, r.projectID, id)
}

func customRoleActionAttributes() map[string]schema.Attribute {
	attributes := reference.Attributes(reference.URN, "URN of the IAM action, for example `urn:action:server:server:all`.")
	urn := attributes[reference.URN].(schema.StringAttribute)
	urn.Validators = []validator.String{customRoleText{notBlank: true}}
	attributes[reference.URN] = urn
	return attributes
}

// buildCustomRoleCreateBody omits enabled when the configuration omits it,
// because the schema default fills the plan with true and the backend applies
// the same default.
func buildCustomRoleCreateBody(plan, config CustomRoleModel) (projectsdk.CustomRoleCreateSchema, diag.Diagnostics) {
	var body projectsdk.CustomRoleCreateSchema
	var diags diag.Diagnostics

	if plan.DisplayName.IsNull() || plan.DisplayName.IsUnknown() {
		diags.AddError("Missing Custom Role display_name", "display_name must be configured and known.")
	}
	actionURNs, actionDiags := customRoleConfiguredActionURNs(plan.Actions)
	diags.Append(actionDiags...)
	if diags.HasError() {
		return body, diags
	}

	body.DisplayName = strings.TrimSpace(plan.DisplayName.ValueString())
	body.ActionUrns = actionURNs
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		body.Description = new(strings.TrimSpace(plan.Description.ValueString()))
	}
	if !config.Enabled.IsNull() && !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() {
		body.Enabled = new(plan.Enabled.ValueBool())
	}
	return body, diags
}

// buildCustomRoleUpdateBody sends only the fields that changed since state.
// A text or URN change in outer whitespace only is not a change, because the
// request trims it. A changed action set is sent whole, because the backend replaces
// the set.
func buildCustomRoleUpdateBody(plan, state CustomRoleModel) (projectsdk.CustomRolePartialUpdateSchema, bool, diag.Diagnostics) {
	var body projectsdk.CustomRolePartialUpdateSchema
	var diags diag.Diagnostics

	if plan.DisplayName.IsNull() || plan.DisplayName.IsUnknown() {
		diags.AddError("Missing Custom Role display_name", "display_name must be configured and known.")
	}
	actionURNs, actionDiags := customRoleConfiguredActionURNs(plan.Actions)
	diags.Append(actionDiags...)
	if diags.HasError() {
		return body, false, diags
	}

	changed := false
	if !compare.SameTrimmed(state.DisplayName, plan.DisplayName.ValueString()) {
		body.DisplayName = new(strings.TrimSpace(plan.DisplayName.ValueString()))
		changed = true
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() &&
		!compare.SameTrimmed(state.Description, plan.Description.ValueString()) {
		body.Description = new(strings.TrimSpace(plan.Description.ValueString()))
		changed = true
	}
	if !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() && !plan.Enabled.Equal(state.Enabled) {
		body.Enabled = new(plan.Enabled.ValueBool())
		changed = true
	}
	if stateURNs, ok := customRoleKnownActionURNs(state.Actions); !ok || !slices.Equal(actionURNs, stateURNs) {
		body.ActionUrns = &actionURNs
		changed = true
	}
	return body, changed, diags
}

// customRoleConfiguredActionURNs returns the sorted action URNs of a planned
// set. The slice is never nil, because the SDK serializes a nil action_urns as
// JSON null instead of the empty list that clears every action.
func customRoleConfiguredActionURNs(actions types.Set) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	urns, ok := customRoleKnownActionURNs(actions)
	if !ok {
		diags.AddError("Missing Custom Role actions", "actions and every actions[*].urn must be configured and known.")
		return nil, diags
	}
	return urns, diags
}

// customRoleKnownActionURNs returns the sorted, trimmed, distinct URNs of an
// action set, and false when the set or any URN is null or unknown. The API
// trims each URN, so URNs that differ only by outer whitespace name the same
// action.
func customRoleKnownActionURNs(actions types.Set) ([]string, bool) {
	if actions.IsNull() || actions.IsUnknown() {
		return nil, false
	}
	urns := make([]string, 0, len(actions.Elements()))
	for _, urn := range reference.Strings(actions, reference.URN) {
		if urn.IsNull() || urn.IsUnknown() {
			return nil, false
		}
		urns = append(urns, strings.TrimSpace(urn.ValueString()))
	}
	slices.Sort(urns)
	return slices.Compact(urns), true
}

// validateCustomRoleActions rejects two actions whose URNs differ only by
// outer whitespace. The API trims each URN, so both name the same action, and
// the role would hold one action where the configuration holds two. A URN
// that is unknown at plan is not checked.
func validateCustomRoleActions(actions types.Set) diag.Diagnostics {
	var diags diag.Diagnostics
	if actions.IsNull() || actions.IsUnknown() {
		return diags
	}
	seen := make(map[string]bool, len(actions.Elements()))
	for _, urn := range reference.Strings(actions, reference.URN) {
		if urn.IsNull() || urn.IsUnknown() {
			continue
		}
		key := strings.TrimSpace(urn.ValueString())
		if seen[key] {
			diags.AddAttributeError(path.Root("actions"), "Duplicate Custom Role action",
				fmt.Sprintf("actions[*].urn %q appears in more than one element of actions once outer whitespace is removed. List each action once.", key))
			continue
		}
		seen[key] = true
	}
	return diags
}

// customRoleAPIErrorDetail renders every error detail of an API error. The
// SDK error text keeps only the first detail, but the backend lists each user
// and group that holds an in-use role, and each invalid action, separately.
func customRoleAPIErrorDetail(err error) string {
	var apiErr *projectsdk.APIError
	if !errors.As(err, &apiErr) || len(apiErr.Errors) < 2 {
		return err.Error()
	}
	details := make([]string, 0, len(apiErr.Errors))
	for _, detail := range apiErr.Errors {
		text := detail.Message
		if detail.Location != "" {
			text = detail.Location + ": " + text
		}
		if detail.Code != "" {
			text += " (" + detail.Code + ")"
		}
		details = append(details, text)
	}
	rendered := strings.Join(details, "; ")
	if apiErr.RequestID != "" {
		rendered += " [request_id=" + apiErr.RequestID + "]"
	}
	return rendered
}

// customRoleText validates a custom role string by its length in characters,
// the unit of the backend limit, and optionally rejects blank values. It
// counts the trimmed value, because the request sends the value trimmed.
type customRoleText struct {
	maxLength int
	notBlank  bool
}

func (v customRoleText) Description(context.Context) string {
	var rules []string
	if v.notBlank {
		rules = append(rules, "must not be empty or whitespace-only")
	}
	if v.maxLength > 0 {
		rules = append(rules, fmt.Sprintf("must be at most %d characters once outer whitespace is removed", v.maxLength))
	}
	return "Value " + strings.Join(rules, " and ") + "."
}

func (v customRoleText) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v customRoleText) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := strings.TrimSpace(req.ConfigValue.ValueString())
	if v.notBlank && value == "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value", fmt.Sprintf("%s must not be empty or whitespace-only.", req.Path))
	}
	if length := utf8.RuneCountInString(value); v.maxLength > 0 && length > v.maxLength {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid value",
			fmt.Sprintf("%s must be at most %d characters once outer whitespace is removed, got %d.", req.Path, v.maxLength, length))
	}
}
