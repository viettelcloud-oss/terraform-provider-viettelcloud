package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/wait"
)

func serverGroupMemberSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	(&ServerGroupMemberResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}

func serverGroupMemberConfiguredModel() ServerGroupMemberResourceModel {
	return ServerGroupMemberResourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:                 types.StringValue(memberTestID.String()),
			Name:               types.StringValue("web-server-1"),
			Address:            types.StringValue("192.168.1.10"),
			ProtocolPort:       types.Int64Value(80),
			Weight:             types.Int64Value(1),
			Backup:             types.BoolValue(false),
			ProvisioningStatus: types.StringValue("ACTIVE"),
			Status:             types.StringValue("ONLINE"),
			CreatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
			UpdatedAt:          types.StringValue("2026-01-01T00:00:00Z"),
		},
		ServerGroup: memberServerGroupReference(memberTestServerGroupID.String()),
	}
}

func memberServerGroupReference(id string) types.Object {
	return reference.Value(reference.ID, types.StringValue(id))
}

// serverGroupMemberNullModel returns a resource model whose attributes are all
// null, the way Terraform starts an import.
func serverGroupMemberNullModel() ServerGroupMemberResourceModel {
	return ServerGroupMemberResourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{
			ID:                 types.StringNull(),
			Name:               types.StringNull(),
			Address:            types.StringNull(),
			ProtocolPort:       types.Int64Null(),
			Weight:             types.Int64Null(),
			Backup:             types.BoolNull(),
			ProvisioningStatus: types.StringNull(),
			Status:             types.StringNull(),
			CreatedAt:          types.StringNull(),
			UpdatedAt:          types.StringNull(),
		},
		ServerGroup: types.ObjectNull(reference.AttributeTypes(reference.ID)),
	}
}

func serverGroupMemberPlan(t *testing.T, model ServerGroupMemberResourceModel) tfsdk.Plan {
	t.Helper()
	resSchema := serverGroupMemberSchema(t)
	plan := tfsdk.Plan{Schema: resSchema}
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func serverGroupMemberState(t *testing.T, model ServerGroupMemberResourceModel) tfsdk.State {
	t.Helper()
	resSchema := serverGroupMemberSchema(t)
	state := tfsdk.State{Schema: resSchema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func TestServerGroupMemberModelMatchesSchema(t *testing.T) {
	t.Parallel()

	resSchema := serverGroupMemberSchema(t)
	plan := tfsdk.Plan{Schema: resSchema}
	model := serverGroupMemberConfiguredModel()
	if diags := plan.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("resource model does not match schema: %v", diags)
	}
}

func TestServerGroupMemberConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	resourceUnderTest, ok := NewServerGroupMemberResource().(*ServerGroupMemberResource)
	if !ok || resourceUnderTest == nil {
		t.Fatal("constructor did not return a ServerGroupMemberResource")
	}

	var metadata resource.MetadataResponse
	resourceUnderTest.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadata)
	if metadata.TypeName != "viettelcloud_lb_member" {
		t.Fatalf("metadata type=%q, want viettelcloud_lb_member", metadata.TypeName)
	}

	var response resource.ConfigureResponse
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected unexpected provider data diagnostic")
	}

	clientResource := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no request expected")
		return nil, nil
	}))
	configured := &providerdata.Configured{
		LoadBalancer: clientResource.client,
		ProjectID:    memberTestProjectID,
		Claims:       keylock.New(),
	}
	response = resource.ConfigureResponse{}
	resourceUnderTest.Configure(context.Background(), resource.ConfigureRequest{ProviderData: configured}, &response)
	if response.Diagnostics.HasError() || resourceUnderTest.client != configured.LoadBalancer || resourceUnderTest.projectID != configured.ProjectID || resourceUnderTest.claims != configured.Claims {
		t.Fatalf("resource was not configured: diagnostics=%v resource=%#v", response.Diagnostics, resourceUnderTest)
	}
}

func TestServerGroupMemberSchemaContract(t *testing.T) {
	t.Parallel()

	resSchema := serverGroupMemberSchema(t)

	// id: Computed, UseStateForUnknown
	idAttr, ok := resSchema.Attributes["id"].(schema.StringAttribute)
	if !ok || !idAttr.Computed || len(idAttr.PlanModifiers) != 1 {
		t.Errorf("id must be computed and UseStateForUnknown, got %#v", resSchema.Attributes["id"])
	}

	// server_group: Required reference object selected by a required id, RequiresReplace
	sgAttr, ok := resSchema.Attributes["server_group"].(schema.SingleNestedAttribute)
	if !ok || !sgAttr.Required || sgAttr.Optional || sgAttr.Computed || len(sgAttr.PlanModifiers) != 1 {
		t.Errorf("server_group must be a required object with RequiresReplace, got %#v", resSchema.Attributes["server_group"])
	} else if inner, ok := sgAttr.Attributes[reference.ID].(schema.StringAttribute); len(sgAttr.Attributes) != 1 || !ok || !inner.Required {
		t.Errorf("server_group attributes = %#v, want one required string id", sgAttr.Attributes)
	}
	if _, ok := resSchema.Attributes["server_group_id"]; ok {
		t.Error("server_group_id must not be a resource argument; use the server_group reference")
	}

	// name: Required, updateable
	nameAttr, ok := resSchema.Attributes["name"].(schema.StringAttribute)
	if !ok || !nameAttr.Required || nameAttr.Computed || len(nameAttr.PlanModifiers) != 0 {
		t.Errorf("name must be required and updateable, got %#v", resSchema.Attributes["name"])
	}

	// address: Required, RequiresReplace
	addrAttr, ok := resSchema.Attributes["address"].(schema.StringAttribute)
	if !ok || !addrAttr.Required || len(addrAttr.PlanModifiers) != 1 {
		t.Errorf("address must be required and RequiresReplace, got %#v", resSchema.Attributes["address"])
	}

	// protocol_port: Required, RequiresReplace, validator Between(1, 65535)
	portAttr, ok := resSchema.Attributes["protocol_port"].(schema.Int64Attribute)
	if !ok || !portAttr.Required || len(portAttr.PlanModifiers) != 1 || len(portAttr.Validators) != 1 {
		t.Errorf("protocol_port must be required, RequiresReplace, and validated, got %#v", resSchema.Attributes["protocol_port"])
	}

	// weight: Optional, Computed, Default: 1
	weightAttr, ok := resSchema.Attributes["weight"].(schema.Int64Attribute)
	if !ok || !weightAttr.Optional || !weightAttr.Computed || weightAttr.Default == nil {
		t.Errorf("weight must be optional, computed, with default, got %#v", resSchema.Attributes["weight"])
	}

	// backup: Optional, Computed, Default: false
	backupAttr, ok := resSchema.Attributes["backup"].(schema.BoolAttribute)
	if !ok || !backupAttr.Optional || !backupAttr.Computed || backupAttr.Default == nil {
		t.Errorf("backup must be optional, computed, with default, got %#v", resSchema.Attributes["backup"])
	}

	// Computed attributes
	for _, attrName := range []string{"provisioning_status", "status", "created_at", "updated_at"} {
		attr, exists := resSchema.Attributes[attrName]
		if !exists {
			t.Errorf("expected computed attribute %q", attrName)
			continue
		}
		strAttr, ok := attr.(schema.StringAttribute)
		if !ok || !strAttr.Computed {
			t.Errorf("attribute %q must be computed StringAttribute, got %#v", attrName, attr)
		}
	}
}

func TestServerGroupMemberProtocolPortValidator(t *testing.T) {
	t.Parallel()

	pv := memberProtocolPortValidator{}

	if pv.Description(context.Background()) == "" || pv.MarkdownDescription(context.Background()) == "" {
		t.Fatal("memberProtocolPortValidator descriptions must not be empty")
	}

	// ProtocolPort tests
	portTests := []struct {
		val   int64
		valid bool
	}{
		{0, false},
		{1, true},
		{80, true},
		{443, true},
		{65535, true},
		{65536, false},
		{-1, false},
	}
	for _, tt := range portTests {
		var resp validator.Int64Response
		pv.ValidateInt64(context.Background(), validator.Int64Request{
			ConfigValue: types.Int64Value(tt.val),
			Path:        path.Root("protocol_port"),
		}, &resp)
		if tt.valid && resp.Diagnostics.HasError() {
			t.Errorf("port %d expected valid, got %v", tt.val, resp.Diagnostics)
		}
		if !tt.valid && !resp.Diagnostics.HasError() {
			t.Errorf("port %d expected invalid, got no error", tt.val)
		}
	}

	// Null and Unknown ports skip validation
	var nullPortResp validator.Int64Response
	pv.ValidateInt64(context.Background(), validator.Int64Request{
		ConfigValue: types.Int64Null(),
		Path:        path.Root("protocol_port"),
	}, &nullPortResp)
	if nullPortResp.Diagnostics.HasError() {
		t.Errorf("null port should not produce error: %v", nullPortResp.Diagnostics)
	}
}

func TestServerGroupMemberPlanModifiersRequireReplace(t *testing.T) {
	t.Parallel()

	resSchema := serverGroupMemberSchema(t)

	// Object attribute: server_group
	sgAttr, ok := resSchema.Attributes["server_group"].(schema.SingleNestedAttribute)
	if !ok || len(sgAttr.PlanModifiers) != 1 {
		t.Fatalf("expected one plan modifier for server_group, got %#v", resSchema.Attributes["server_group"])
	}
	sgBefore := serverGroupMemberConfiguredModel()
	sgBefore.ServerGroup = memberServerGroupReference("00000000-0000-0000-0000-000000000001")
	sgAfter := serverGroupMemberConfiguredModel()
	sgAfter.ServerGroup = memberServerGroupReference("00000000-0000-0000-0000-000000000002")
	var sgResponse planmodifier.ObjectResponse
	sgAttr.PlanModifiers[0].PlanModifyObject(context.Background(), planmodifier.ObjectRequest{
		State:       serverGroupMemberState(t, sgBefore),
		Plan:        serverGroupMemberPlan(t, sgAfter),
		StateValue:  sgBefore.ServerGroup,
		PlanValue:   sgAfter.ServerGroup,
		ConfigValue: sgAfter.ServerGroup,
	}, &sgResponse)
	if !sgResponse.RequiresReplace {
		t.Error("expected changing server_group.id to require replacement")
	}
	sgPadded := serverGroupMemberConfiguredModel()
	sgPadded.ServerGroup = memberServerGroupReference(" 00000000-0000-0000-0000-000000000001\n")
	sgResponse = planmodifier.ObjectResponse{}
	sgAttr.PlanModifiers[0].PlanModifyObject(context.Background(), planmodifier.ObjectRequest{
		State:       serverGroupMemberState(t, sgBefore),
		Plan:        serverGroupMemberPlan(t, sgPadded),
		StateValue:  sgBefore.ServerGroup,
		PlanValue:   sgPadded.ServerGroup,
		ConfigValue: sgPadded.ServerGroup,
	}, &sgResponse)
	if sgResponse.RequiresReplace {
		t.Error("a padded server_group.id must not require replacement")
	}

	// String attribute: address
	for _, name := range []string{"address"} {
		attr, ok := resSchema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a string attribute", name)
		}
		if len(attr.PlanModifiers) != 1 {
			t.Fatalf("expected one plan modifier for %s, got %d", name, len(attr.PlanModifiers))
		}

		modelBefore := serverGroupMemberConfiguredModel()
		modelAfter := serverGroupMemberConfiguredModel()
		var stateValue, planValue types.String
		switch name {
		case "address":
			stateValue = types.StringValue("192.168.1.10")
			planValue = types.StringValue("192.168.1.20")
			modelBefore.Address = stateValue
			modelAfter.Address = planValue
		}

		state := serverGroupMemberState(t, modelBefore)
		plan := serverGroupMemberPlan(t, modelAfter)

		request := planmodifier.StringRequest{
			State:       state,
			Plan:        plan,
			StateValue:  stateValue,
			PlanValue:   planValue,
			ConfigValue: planValue,
		}
		var response planmodifier.StringResponse
		attr.PlanModifiers[0].PlanModifyString(context.Background(), request, &response)
		if !response.RequiresReplace {
			t.Errorf("expected changing %s to require replacement", name)
		}

		request.PlanValue = types.StringValue(" " + stateValue.ValueString() + "\t")
		request.ConfigValue = request.PlanValue
		response = planmodifier.StringResponse{}
		attr.PlanModifiers[0].PlanModifyString(context.Background(), request, &response)
		if response.RequiresReplace {
			t.Errorf("a padded %s must not require replacement", name)
		}
	}

	// Int64 attribute: protocol_port
	portAttr, ok := resSchema.Attributes["protocol_port"].(schema.Int64Attribute)
	if !ok {
		t.Fatalf("protocol_port is not an int64 attribute")
	}
	if len(portAttr.PlanModifiers) != 1 {
		t.Fatalf("expected one plan modifier for protocol_port, got %d", len(portAttr.PlanModifiers))
	}

	modelBefore := serverGroupMemberConfiguredModel()
	modelBefore.ProtocolPort = types.Int64Value(80)
	modelAfter := serverGroupMemberConfiguredModel()
	modelAfter.ProtocolPort = types.Int64Value(443)

	state := serverGroupMemberState(t, modelBefore)
	plan := serverGroupMemberPlan(t, modelAfter)

	request := planmodifier.Int64Request{
		State:       state,
		Plan:        plan,
		StateValue:  modelBefore.ProtocolPort,
		PlanValue:   modelAfter.ProtocolPort,
		ConfigValue: modelAfter.ProtocolPort,
	}
	var response planmodifier.Int64Response
	portAttr.PlanModifiers[0].PlanModifyInt64(context.Background(), request, &response)
	if !response.RequiresReplace {
		t.Errorf("expected changing protocol_port to require replacement")
	}
}

func TestServerGroupMemberPopulateResourceStatePreservesIPAddress(t *testing.T) {
	t.Parallel()

	// Case 1: IPv6 uncompressed vs compressed
	configuredIPv6 := "2001:0db8:0000:0000:0000:0000:0000:0001"
	apiIPv6 := "2001:db8::1"
	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", apiIPv6, 80, 1, false)

	state := ServerGroupMemberResourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{Address: types.StringValue(configuredIPv6)},
		ServerGroup:            memberServerGroupReference(memberTestServerGroupID.String()),
	}
	populateServerGroupMemberResourceState(member, &state)
	if state.Address.ValueString() != configuredIPv6 {
		t.Fatalf("expected configured IPv6 address %q to be preserved, got %q", configuredIPv6, state.Address.ValueString())
	}

	// Case 2: IPv4 unchanged
	configuredIPv4 := "192.168.1.10"
	memberIPv4 := sampleServerGroupMemberDetail(memberTestID, "web-server-1", configuredIPv4, 80, 1, false)
	stateIPv4 := ServerGroupMemberResourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{Address: types.StringValue(configuredIPv4)},
		ServerGroup:            memberServerGroupReference(memberTestServerGroupID.String()),
	}
	populateServerGroupMemberResourceState(memberIPv4, &stateIPv4)
	if stateIPv4.Address.ValueString() != configuredIPv4 {
		t.Fatalf("expected configured IPv4 address %q to be preserved, got %q", configuredIPv4, stateIPv4.Address.ValueString())
	}

	// Case 3: Real drift in address is not hidden
	driftedMember := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.99", 80, 1, false)
	stateDrift := ServerGroupMemberResourceModel{
		ServerGroupMemberModel: ServerGroupMemberModel{Address: types.StringValue("192.168.1.10")},
		ServerGroup:            memberServerGroupReference(memberTestServerGroupID.String()),
	}
	populateServerGroupMemberResourceState(driftedMember, &stateDrift)
	if stateDrift.Address.ValueString() != "192.168.1.99" {
		t.Fatalf("expected drifted address 192.168.1.99 to be set, got %q", stateDrift.Address.ValueString())
	}
}

func TestServerGroupMemberCreate(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	calls := 0
	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method == http.MethodPost && strings.Contains(req.URL.Path, fmt.Sprintf("/v2/load-balancing/server-groups/%s/members/", memberTestServerGroupID)) {
			var body loadbalancersdk.ServerGroupMemberCreateSchema
			b, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(b, &body)
			if body.Name != "web-server-1" || body.Address != "192.168.1.10" || body.ProtocolPort != 80 {
				t.Fatalf("unexpected create body: %#v", body)
			}
			var raw map[string]any
			_ = json.Unmarshal(b, &raw)
			if _, ok := raw["server_group_id"]; ok {
				t.Fatalf("create body must not carry server_group_id; the path names the group: %s", b)
			}
			return serverGroupMemberHTTPResponse(req, http.StatusCreated, memberJSON), nil
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	plan := serverGroupMemberConfiguredModel()
	plan.Name = types.StringValue("  web-server-1 ")
	plan.ID = types.StringUnknown()
	plan.ProvisioningStatus = types.StringUnknown()
	plan.Status = types.StringUnknown()
	plan.CreatedAt = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()

	resSchema := serverGroupMemberSchema(t)
	req := resource.CreateRequest{
		Plan: serverGroupMemberPlan(t, plan),
	}

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: resSchema}

	r.Create(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error creating member: %v", resp.Diagnostics)
	}

	var state ServerGroupMemberResourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("failed to decode state: %v", diags)
	}

	if state.ID.ValueString() != memberTestID.String() {
		t.Fatalf("state id=%q, want %q", state.ID.ValueString(), memberTestID.String())
	}
	if state.Name.ValueString() != "  web-server-1 " {
		t.Fatalf("state name=%q, want the configured representation kept", state.Name.ValueString())
	}
	if !state.ServerGroup.Equal(memberServerGroupReference(memberTestServerGroupID.String())) {
		t.Fatalf("state server_group=%s, want the configured reference", state.ServerGroup)
	}
	if state.Address.ValueString() != "192.168.1.10" {
		t.Fatalf("state address=%q, want 192.168.1.10", state.Address.ValueString())
	}
	if state.ProtocolPort.ValueInt64() != 80 {
		t.Fatalf("state protocol_port=%d, want 80", state.ProtocolPort.ValueInt64())
	}
}

// The API does not accept an address with outer whitespace and trims name, so
// Create sends trimmed values. State keeps the configured spelling through
// Create and the next Read.
func TestServerGroupMemberCreateTrimsStringsAndKeepsConfiguredSpelling(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		configured string
		sent       string
		backend    string
	}{
		{name: "IPv4", configured: " 192.168.1.10\n", sent: "192.168.1.10", backend: "192.168.1.10"},
		{name: "IPv6", configured: "\t2001:DB8:0::10 ", sent: "2001:DB8:0::10", backend: "2001:db8::10"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			memberJSON := sampleServerGroupMemberDetailJSON(t, sampleServerGroupMemberDetail(memberTestID, "web-server-1", tt.backend, 80, 1, false))
			var sent []string
			r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost && strings.Contains(req.URL.Path, fmt.Sprintf("/v2/load-balancing/server-groups/%s/members/", memberTestServerGroupID)) {
					var body loadbalancersdk.ServerGroupMemberCreateSchema
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatalf("decode create body: %v", err)
					}
					if body.Name != "web-server-1" {
						t.Errorf("create body name = %q, want %q", body.Name, "web-server-1")
					}
					sent = append(sent, body.Address)
					return serverGroupMemberHTTPResponse(req, http.StatusCreated, memberJSON), nil
				}
				if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
					return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
				}
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				return nil, nil
			}))

			plan := serverGroupMemberConfiguredModel()
			plan.Address = types.StringValue(tt.configured)
			plan.Name = types.StringValue(" web-server-1 ")
			plan.ServerGroup = memberServerGroupReference("  " + strings.ToUpper(memberTestServerGroupID.String()) + " ")
			plan.ID = types.StringUnknown()
			plan.ProvisioningStatus = types.StringUnknown()
			plan.Status = types.StringUnknown()
			plan.CreatedAt = types.StringUnknown()
			plan.UpdatedAt = types.StringUnknown()
			create := resource.CreateResponse{State: tfsdk.State{Schema: serverGroupMemberSchema(t)}}
			r.Create(context.Background(), resource.CreateRequest{Plan: serverGroupMemberPlan(t, plan)}, &create)
			if create.Diagnostics.HasError() {
				t.Fatalf("unexpected error creating member: %v", create.Diagnostics)
			}
			if want := []string{tt.sent}; !slices.Equal(sent, want) {
				t.Fatalf("sent addresses = %q, want %q", sent, want)
			}
			var created ServerGroupMemberResourceModel
			if diags := create.State.Get(context.Background(), &created); diags.HasError() {
				t.Fatalf("failed to decode create state: %v", diags)
			}
			if created.Address.ValueString() != tt.configured || !created.Name.Equal(plan.Name) || !created.ServerGroup.Equal(plan.ServerGroup) {
				t.Fatalf("create state = %#v, want the configured spellings", created)
			}

			read := resource.ReadResponse{State: create.State}
			r.Read(context.Background(), resource.ReadRequest{State: create.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatalf("unexpected error reading member: %v", read.Diagnostics)
			}
			var refreshed ServerGroupMemberResourceModel
			if diags := read.State.Get(context.Background(), &refreshed); diags.HasError() {
				t.Fatalf("failed to decode read state: %v", diags)
			}
			if refreshed.Address.ValueString() != tt.configured || !refreshed.Name.Equal(plan.Name) || !refreshed.ServerGroup.Equal(plan.ServerGroup) {
				t.Fatalf("read state = %#v, want the configured spellings", refreshed)
			}
		})
	}
}

// The backend accepts weights 1 to 256, so a configured 0 is sent as is and
// the refusal comes back as a diagnostic.
func TestServerGroupMemberCreateZeroWeightRejected(t *testing.T) {
	t.Parallel()

	posts := 0
	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		posts++
		b, _ := io.ReadAll(req.Body)
		var raw map[string]any
		_ = json.Unmarshal(b, &raw)
		if weight, ok := raw["weight"]; !ok || weight != float64(0) {
			t.Fatalf("create body weight=%v (present=%t), want an explicit 0: %s", weight, ok, b)
		}
		if backup, ok := raw["backup"]; !ok || backup != false {
			t.Fatalf("create body backup=%v (present=%t), want an explicit false: %s", backup, ok, b)
		}
		return serverGroupMemberHTTPResponse(req, http.StatusBadRequest, `{"errors":[{"code":"invalid_input","message":"weight: Ensure this value is greater than or equal to 1."}]}`), nil
	}))

	plan := serverGroupMemberConfiguredModel()
	plan.Weight = types.Int64Value(0)
	plan.Backup = types.BoolValue(false)
	plan.ID = types.StringUnknown()
	plan.ProvisioningStatus = types.StringUnknown()
	plan.Status = types.StringUnknown()
	plan.CreatedAt = types.StringUnknown()
	plan.UpdatedAt = types.StringUnknown()

	req := resource.CreateRequest{Plan: serverGroupMemberPlan(t, plan)}
	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: serverGroupMemberSchema(t)}

	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "greater than or equal to 1") {
		t.Fatalf("expected the backend weight error as a diagnostic, got %v", resp.Diagnostics)
	}
	if posts != 1 {
		t.Fatalf("expected the rejected create to be sent once, got %d posts", posts)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatalf("a rejected create must not write state, got %s", resp.State.Raw)
	}
}

// Section 4.2 of design/loadbalancer/server_group_member.md makes
// (address, protocol_port) unique in one Server Group. The status of the
// refusal is not confirmed against the real API, so both candidates are
// covered. Neither is a busy refusal.
func TestServerGroupMemberCreateDuplicateAddressPortRejected(t *testing.T) {
	t.Parallel()

	const message = "Server Group Member with this Server group, Address and Protocol port already exists."
	refusals := []struct {
		name   string
		status int
		code   string
	}{
		{"400 invalid_input", http.StatusBadRequest, "invalid_input"},
		{"409 conflict", http.StatusConflict, "conflict"},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()

			posts := 0
			r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
				posts++
				body := fmt.Sprintf(`{"errors":[{"location":"","code":%q,"message":%q}]}`, refusal.code, message)
				return serverGroupMemberHTTPResponse(req, refusal.status, body), nil
			}))

			plan := serverGroupMemberConfiguredModel()
			plan.ID = types.StringUnknown()
			plan.ProvisioningStatus = types.StringUnknown()
			plan.Status = types.StringUnknown()
			plan.CreatedAt = types.StringUnknown()
			plan.UpdatedAt = types.StringUnknown()

			req := resource.CreateRequest{Plan: serverGroupMemberPlan(t, plan)}
			var resp resource.CreateResponse
			resp.State = tfsdk.State{Schema: serverGroupMemberSchema(t)}

			r.Create(context.Background(), req, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected the duplicate address and port to fail the create")
			}
			got := resp.Diagnostics.Errors()[0]
			if got.Summary() != "Error creating server group member" || !strings.Contains(got.Detail(), message) {
				t.Fatalf("diagnostic=%q: %q, want the backend refusal", got.Summary(), got.Detail())
			}
			if strings.Contains(got.Detail(), "refused as busy") {
				t.Fatalf("diagnostic detail=%q, want no busy refusal", got.Detail())
			}
			if posts != 1 {
				t.Fatalf("expected the rejected create to be sent once, got %d posts", posts)
			}
			if !resp.State.Raw.IsNull() {
				t.Fatalf("a rejected create must not write state, got %s", resp.State.Raw)
			}
		})
	}
}

func TestServerGroupMemberCreateMissingOrEmptyFields(t *testing.T) {
	t.Parallel()

	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no network call expected")
		return nil, nil
	}))

	resSchema := serverGroupMemberSchema(t)

	// Invalid server_group.id
	plan := serverGroupMemberConfiguredModel()
	plan.ServerGroup = memberServerGroupReference("invalid-uuid")
	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: resSchema}
	req := resource.CreateRequest{Plan: serverGroupMemberPlan(t, plan)}
	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error for invalid server_group.id")
	}

	// Empty name
	plan = serverGroupMemberConfiguredModel()
	plan.Name = types.StringValue("   ")
	resp = resource.CreateResponse{State: tfsdk.State{Schema: resSchema}}
	req = resource.CreateRequest{Plan: serverGroupMemberPlan(t, plan)}
	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "name must be configured") {
		t.Fatalf("expected error for empty name, got %v", resp.Diagnostics)
	}

	// Empty address
	plan = serverGroupMemberConfiguredModel()
	plan.Address = types.StringValue("   ")
	resp = resource.CreateResponse{State: tfsdk.State{Schema: resSchema}}
	req = resource.CreateRequest{Plan: serverGroupMemberPlan(t, plan)}
	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "address must be configured") {
		t.Fatalf("expected error for empty address, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberCreateWaitTerminalError(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	member.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusERROR
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPost {
			return serverGroupMemberHTTPResponse(req, http.StatusCreated, memberJSON), nil
		}
		if req.Method == http.MethodGet {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	plan := serverGroupMemberConfiguredModel()
	resSchema := serverGroupMemberSchema(t)
	req := resource.CreateRequest{Plan: serverGroupMemberPlan(t, plan)}
	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: resSchema}

	r.Create(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error waiting for member to become active")
	}
	if !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "marked tainted") {
		t.Fatalf("expected diagnostic noting tainted state, got %v", resp.Diagnostics)
	}
	var state ServerGroupMemberResourceModel
	if diags := resp.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if state.ID.ValueString() != memberTestID.String() {
		t.Fatalf("state ID=%q, want %q", state.ID.ValueString(), memberTestID.String())
	}
	if state.ProvisioningStatus.IsUnknown() || state.Status.IsUnknown() ||
		state.CreatedAt.IsUnknown() || state.UpdatedAt.IsUnknown() {
		t.Fatalf("state keeps unknown computed values after the waiter failed: %#v", state)
	}
}

func TestServerGroupMemberRead(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	state := serverGroupMemberConfiguredModel()
	req := resource.ReadRequest{
		State: serverGroupMemberState(t, state),
	}
	var resp resource.ReadResponse
	resp.State = serverGroupMemberState(t, state)

	r.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error reading member: %v", resp.Diagnostics)
	}

	var updatedState ServerGroupMemberResourceModel
	_ = resp.State.Get(context.Background(), &updatedState)
	if updatedState.ID.ValueString() != memberTestID.String() {
		t.Fatalf("id=%q, want %q", updatedState.ID.ValueString(), memberTestID.String())
	}
}

func TestServerGroupMemberReadNotFound(t *testing.T) {
	t.Parallel()

	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	state := serverGroupMemberConfiguredModel()
	req := resource.ReadRequest{
		State: serverGroupMemberState(t, state),
	}
	var resp resource.ReadResponse
	resp.State = serverGroupMemberState(t, state)

	r.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on not found: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}
}

func TestServerGroupMemberReadDeletedStatus(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	member.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	state := serverGroupMemberConfiguredModel()
	req := resource.ReadRequest{
		State: serverGroupMemberState(t, state),
	}
	var resp resource.ReadResponse
	resp.State = serverGroupMemberState(t, state)

	r.Read(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on DELETED read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("expected state to be removed when provisioning_status is DELETED")
	}
}

func TestServerGroupMemberUpdate(t *testing.T) {
	t.Parallel()

	updatedMember := sampleServerGroupMemberDetail(memberTestID, "web-server-renamed", "192.168.1.10", 80, 5, true)
	updatedMemberJSON := sampleServerGroupMemberDetailJSON(t, updatedMember)

	patchCalled := false
	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			patchCalled = true
			var body loadbalancersdk.ServerGroupMemberPartialUpdateSchema
			b, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(b, &body)
			if body.Name == nil || *body.Name != "web-server-renamed" ||
				body.Weight == nil || *body.Weight != 5 ||
				body.Backup == nil || *body.Backup != true {
				t.Fatalf("unexpected update body: %#v", body)
			}
			return serverGroupMemberHTTPResponse(req, http.StatusOK, updatedMemberJSON), nil
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, updatedMemberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	state := serverGroupMemberConfiguredModel()
	plan := serverGroupMemberConfiguredModel()
	plan.Name = types.StringValue("web-server-renamed")
	plan.Weight = types.Int64Value(5)
	plan.Backup = types.BoolValue(true)

	resSchema := serverGroupMemberSchema(t)
	req := resource.UpdateRequest{
		Plan:  serverGroupMemberPlan(t, plan),
		State: serverGroupMemberState(t, state),
	}
	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: resSchema}

	r.Update(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error updating member: %v", resp.Diagnostics)
	}
	if !patchCalled {
		t.Fatal("expected PATCH request to be called")
	}

	var newState ServerGroupMemberResourceModel
	_ = resp.State.Get(context.Background(), &newState)
	if newState.Name.ValueString() != "web-server-renamed" || newState.Weight.ValueInt64() != 5 || !newState.Backup.ValueBool() {
		t.Fatalf("unexpected updated state: %#v", newState)
	}
}

func TestServerGroupMemberUpdateBackToDefaultWeightAndBackup(t *testing.T) {
	t.Parallel()

	resetMember := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	resetJSON := sampleServerGroupMemberDetailJSON(t, resetMember)

	patchCalled := false
	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			patchCalled = true
			b, _ := io.ReadAll(req.Body)
			var raw map[string]any
			_ = json.Unmarshal(b, &raw)
			if weight, ok := raw["weight"]; !ok || weight != float64(1) {
				t.Fatalf("patch body weight=%v (present=%t), want 1: %s", weight, ok, b)
			}
			if backup, ok := raw["backup"]; !ok || backup != false {
				t.Fatalf("patch body backup=%v (present=%t), want an explicit false: %s", backup, ok, b)
			}
			if _, ok := raw["name"]; ok {
				t.Fatalf("patch body must omit the unchanged name: %s", b)
			}
			return serverGroupMemberHTTPResponse(req, http.StatusOK, resetJSON), nil
		}
		return serverGroupMemberHTTPResponse(req, http.StatusOK, resetJSON), nil
	}))

	state := serverGroupMemberConfiguredModel()
	state.Weight = types.Int64Value(5)
	state.Backup = types.BoolValue(true)
	plan := serverGroupMemberConfiguredModel()

	req := resource.UpdateRequest{
		Plan:  serverGroupMemberPlan(t, plan),
		State: serverGroupMemberState(t, state),
	}
	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: serverGroupMemberSchema(t)}

	r.Update(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error updating member: %v", resp.Diagnostics)
	}
	if !patchCalled {
		t.Fatal("expected PATCH request to be called")
	}
	var newState ServerGroupMemberResourceModel
	_ = resp.State.Get(context.Background(), &newState)
	if newState.ID.ValueString() != memberTestID.String() || newState.Weight.ValueInt64() != 1 || newState.Backup.ValueBool() {
		t.Fatalf("unexpected state after reset: %#v", newState)
	}
}

func TestServerGroupMemberUpdateNoChanges(t *testing.T) {
	t.Parallel()

	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	memberJSON := sampleServerGroupMemberDetailJSON(t, member)

	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodPatch {
			t.Fatal("PATCH must not be called when there are no changes")
		}
		if req.Method == http.MethodGet {
			return serverGroupMemberHTTPResponse(req, http.StatusOK, memberJSON), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	// The API trims name, so a change of outer whitespace alone is no change.
	for _, name := range []string{"web-server-1", " web-server-1\n"} {
		state := serverGroupMemberConfiguredModel()
		plan := serverGroupMemberConfiguredModel()
		plan.Name = types.StringValue(name)

		resSchema := serverGroupMemberSchema(t)
		req := resource.UpdateRequest{
			Plan:  serverGroupMemberPlan(t, plan),
			State: serverGroupMemberState(t, state),
		}
		var resp resource.UpdateResponse
		resp.State = tfsdk.State{Schema: resSchema}

		r.Update(context.Background(), req, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on no-change update of name %q: %v", name, resp.Diagnostics)
		}
		var updated ServerGroupMemberResourceModel
		if diags := resp.State.Get(context.Background(), &updated); diags.HasError() || !updated.Name.Equal(plan.Name) {
			t.Fatalf("updated name = %s, want %s (diagnostics %v)", updated.Name, plan.Name, diags)
		}
	}
}

func TestServerGroupMemberUpdateInvalidName(t *testing.T) {
	t.Parallel()

	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no network call expected")
		return nil, nil
	}))

	state := serverGroupMemberConfiguredModel()
	plan := serverGroupMemberConfiguredModel()
	plan.Name = types.StringValue("   ")

	resSchema := serverGroupMemberSchema(t)
	req := resource.UpdateRequest{
		Plan:  serverGroupMemberPlan(t, plan),
		State: serverGroupMemberState(t, state),
	}
	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: resSchema}

	r.Update(context.Background(), req, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "name must not be empty") {
		t.Fatalf("expected error for whitespace name, got %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberUpdateZeroWeightRejected(t *testing.T) {
	t.Parallel()

	patches := 0
	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
			return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
		}
		if req.Method != http.MethodPatch {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		patches++
		b, _ := io.ReadAll(req.Body)
		var raw map[string]any
		_ = json.Unmarshal(b, &raw)
		if weight, ok := raw["weight"]; !ok || weight != float64(0) {
			t.Fatalf("patch body weight=%v (present=%t), want an explicit 0: %s", weight, ok, b)
		}
		return serverGroupMemberHTTPResponse(req, http.StatusBadRequest, `{"errors":[{"code":"invalid_input","message":"weight: Ensure this value is greater than or equal to 1."}]}`), nil
	}))

	state := serverGroupMemberConfiguredModel()
	plan := serverGroupMemberConfiguredModel()
	plan.Weight = types.Int64Value(0)

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: serverGroupMemberSchema(t)}
	r.Update(context.Background(), resource.UpdateRequest{
		Plan:  serverGroupMemberPlan(t, plan),
		State: serverGroupMemberState(t, state),
	}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "greater than or equal to 1") {
		t.Fatalf("expected the backend weight error as a diagnostic, got %v", resp.Diagnostics)
	}
	if patches != 1 {
		t.Fatalf("expected the rejected update to be sent once, got %d patches", patches)
	}
}

func TestServerGroupMemberDelete(t *testing.T) {
	t.Parallel()

	deleteCalled := false
	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			deleteCalled = true
			return serverGroupMemberHTTPResponse(req, http.StatusAccepted, ""), nil
		}
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, fmt.Sprintf("/members/%s/", memberTestID)) {
			if deleteCalled {
				return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
			}
			member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
			return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	state := serverGroupMemberConfiguredModel()
	req := resource.DeleteRequest{
		State: serverGroupMemberState(t, state),
	}
	var resp resource.DeleteResponse

	r.Delete(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error deleting member: %v", resp.Diagnostics)
	}
	if !deleteCalled {
		t.Fatal("expected DELETE request to be called")
	}
}

func TestServerGroupMemberDeleteNotFoundIsSuccess(t *testing.T) {
	t.Parallel()

	// The idle wait still sees the Member, and the DELETE then finds it gone.
	r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete {
			return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		if req.Method == http.MethodGet {
			member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
			return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	}))

	state := serverGroupMemberConfiguredModel()
	req := resource.DeleteRequest{State: serverGroupMemberState(t, state)}
	var resp resource.DeleteResponse

	r.Delete(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error deleting non-existent member: %v", resp.Diagnostics)
	}
}

func TestServerGroupMemberImportState(t *testing.T) {
	t.Parallel()

	r := &ServerGroupMemberResource{}

	// Valid import ID
	validID := fmt.Sprintf("%s/%s", memberTestServerGroupID, memberTestID)
	var resp resource.ImportStateResponse
	resSchema := serverGroupMemberSchema(t)
	resp.State = tfsdk.State{Schema: resSchema}
	nullModel := serverGroupMemberNullModel()
	if diags := resp.State.Set(context.Background(), &nullModel); diags.HasError() {
		t.Fatalf("initialize import state: %v", diags)
	}

	r.ImportState(context.Background(), resource.ImportStateRequest{ID: validID}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on valid import: %v", resp.Diagnostics)
	}

	var state ServerGroupMemberResourceModel
	_ = resp.State.Get(context.Background(), &state)
	if !state.ServerGroup.Equal(memberServerGroupReference(memberTestServerGroupID.String())) || state.ID.ValueString() != memberTestID.String() {
		t.Fatalf("imported state mismatch: server_group=%s id=%q", state.ServerGroup, state.ID.ValueString())
	}

	// Invalid import IDs
	invalidIDs := []string{
		"",
		"just-an-id",
		"invalid-group/invalid-member",
		"too/many/parts/here",
		"   /   ",
		fmt.Sprintf("%s/", memberTestServerGroupID),
		fmt.Sprintf("/%s", memberTestID),
	}
	for _, id := range invalidIDs {
		resp = resource.ImportStateResponse{State: tfsdk.State{Schema: resSchema}}
		_ = resp.State.Set(context.Background(), &nullModel)
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("expected error for invalid import ID %q", id)
		}
	}
}

func TestServerGroupMemberWaitUntilReady(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
			if calls == 1 {
				member.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGCREATE
			} else {
				member.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
			}
			return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
		}))

		start := time.Now()
		member, err := r.waitUntilReady(
			context.Background(),
			memberTestServerGroupID,
			memberTestID,
			serverGroupMemberReadyTimeout,
			serverGroupMemberReadyPollInterval,
		)
		if err != nil {
			t.Fatalf("unexpected waiter error: %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
		if waited := time.Since(start); waited != serverGroupMemberReadyPollInterval {
			t.Fatalf("expected elapsed wait of %s, got %s", serverGroupMemberReadyPollInterval, waited)
		}
		if member.ProvisioningStatus != loadbalancersdk.LoadBalancingProvisioningStatusACTIVE {
			t.Fatalf("member status=%s, want ACTIVE", member.ProvisioningStatus)
		}
	})
}

// The backend never leaves UNKNOWN on its own, so the waiter treats it like
// ERROR and fails without waiting.
func TestServerGroupMemberWaitUntilReadyTerminalError(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
					member.ProvisioningStatus = status
					return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
				}))

				start := time.Now()
				_, err := r.waitUntilReady(
					context.Background(),
					memberTestServerGroupID,
					memberTestID,
					serverGroupMemberReadyTimeout,
					serverGroupMemberReadyPollInterval,
				)
				if err == nil || !strings.Contains(err.Error(), "terminal provisioning status "+string(status)) {
					t.Fatalf("expected terminal status error, got %v", err)
				}
				if waited := time.Since(start); waited != 0 {
					t.Fatalf("waited %s, want an immediate failure", waited)
				}
			})
		})
	}
}

func TestServerGroupMemberWaitUntilDeleted(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		calls := 0
		r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
				member.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE
				return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
			}
			return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}))

		start := time.Now()
		err := r.waitUntilDeleted(
			context.Background(),
			memberTestServerGroupID,
			memberTestID,
			serverGroupMemberReadyTimeout,
			serverGroupMemberReadyPollInterval,
		)
		if err != nil {
			t.Fatalf("unexpected waiter error: %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
		if waited := time.Since(start); waited != serverGroupMemberReadyPollInterval {
			t.Fatalf("expected elapsed wait of %s, got %s", serverGroupMemberReadyPollInterval, waited)
		}
	})
}

func TestServerGroupMemberWaitUntilDeletedStatus(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
			member.ProvisioningStatus = loadbalancersdk.LoadBalancingProvisioningStatusDELETED
			return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
		}))

		err := r.waitUntilDeleted(
			context.Background(),
			memberTestServerGroupID,
			memberTestID,
			serverGroupMemberReadyTimeout,
			serverGroupMemberReadyPollInterval,
		)
		if err != nil {
			t.Fatalf("unexpected waiter error: %v", err)
		}
	})
}

// A Delete can start from ERROR or UNKNOWN, so the waiter fails on either
// only after it has seen PENDING_DELETE.
func TestServerGroupMemberWaitUntilDeletedFailedStates(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				statuses := []loadbalancersdk.LoadBalancingProvisioningStatus{
					status,
					loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE,
					status,
				}
				gets := 0
				r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					gets++
					member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
					member.ProvisioningStatus = memberTestStatus(statuses, gets)
					return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
				}))

				start := time.Now()
				err := r.waitUntilDeleted(
					context.Background(),
					memberTestServerGroupID,
					memberTestID,
					serverGroupMemberReadyTimeout,
					serverGroupMemberReadyPollInterval,
				)
				if err == nil || !strings.Contains(err.Error(), "terminal provisioning status "+string(status)+" while deleting") {
					t.Fatalf("expected a failed deletion, got %v", err)
				}
				if waited := time.Since(start); waited != 2*serverGroupMemberReadyPollInterval {
					t.Fatalf("waited %s, want two poll intervals", waited)
				}
			})
		})
	}
}

// The GET of a member in ERROR or UNKNOWN before the DELETE keeps that state
// until the infrastructure accepts the deletion. Without PENDING_DELETE, the
// waiter cannot tell it from a failed deletion, so it waits until timeout.
func TestServerGroupMemberWaitUntilDeletedWithoutPendingDeleteWaitsUntilTimeout(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
					member.ProvisioningStatus = status
					return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
				}))

				start := time.Now()
				err := r.waitUntilDeleted(
					context.Background(),
					memberTestServerGroupID,
					memberTestID,
					serverGroupMemberReadyTimeout,
					serverGroupMemberReadyPollInterval,
				)
				if !errors.Is(err, wait.ErrTimeout) || !strings.Contains(err.Error(), "provisioning_status="+string(status)) {
					t.Fatalf("expected a timeout that names %s, got %v", status, err)
				}
				if waited := time.Since(start); waited != serverGroupMemberReadyTimeout {
					t.Fatalf("waited %s, want %s", waited, serverGroupMemberReadyTimeout)
				}
			})
		})
	}
}

// A failed Delete of a member that started in ERROR or UNKNOWN reports the
// failure and keeps the state. After PENDING_DELETE, the failure shows at once.
// Without PENDING_DELETE, Delete cannot tell it from the state before the
// DELETE, so it waits until timeout.
func TestServerGroupMemberDeleteFromFailedStateKeepsState(t *testing.T) {
	t.Parallel()

	for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusERROR,
		loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
	} {
		for name, tt := range map[string]struct {
			afterDelete []loadbalancersdk.LoadBalancingProvisioningStatus
			wantErr     string
			wantWait    time.Duration
			wantTimeout bool
		}{
			"fails at once after PENDING_DELETE": {
				afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{loadbalancersdk.LoadBalancingProvisioningStatusPENDINGDELETE, status},
				wantErr:     "terminal provisioning status " + string(status) + " while deleting",
				wantWait:    serverGroupMemberReadyPollInterval,
			},
			"waits until timeout without PENDING_DELETE": {
				afterDelete: []loadbalancersdk.LoadBalancingProvisioningStatus{status},
				wantErr:     "provisioning_status=" + string(status),
				wantWait:    serverGroupMemberReadyTimeout,
				wantTimeout: true,
			},
		} {
			t.Run(string(status)+" "+name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					deleted := false
					polls := 0
					r := newServerGroupMemberTestResource(t, loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
						memberPath := fmt.Sprintf("/members/%s/", memberTestID)
						switch {
						case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, memberPath):
							deleted = true
							return serverGroupMemberHTTPResponse(req, http.StatusAccepted, ""), nil
						case req.Method == http.MethodGet && strings.Contains(req.URL.Path, memberPath):
							current := status
							if deleted {
								current = tt.afterDelete[min(polls, len(tt.afterDelete)-1)]
								polls++
							}
							member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
							member.ProvisioningStatus = current
							return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(t, member)), nil
						}
						t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
						return nil, nil
					}))

					state := serverGroupMemberState(t, serverGroupMemberConfiguredModel())
					start := time.Now()
					resp := resource.DeleteResponse{State: state}
					r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
					if !deleted {
						t.Fatal("expected the DELETE to be sent")
					}
					errs := resp.Diagnostics.Errors()
					if len(errs) != 1 || errs[0].Summary() != "Error waiting for server group member deletion" || !strings.Contains(errs[0].Detail(), tt.wantErr) {
						t.Fatalf("expected a deletion wait error containing %q, got %v", tt.wantErr, resp.Diagnostics)
					}
					if timedOut := strings.Contains(errs[0].Detail(), wait.ErrTimeout.Error()); timedOut != tt.wantTimeout {
						t.Fatalf("timeout = %t, want %t: %s", timedOut, tt.wantTimeout, errs[0].Detail())
					}
					if waited := time.Since(start); waited != tt.wantWait {
						t.Fatalf("waited %s, want %s", waited, tt.wantWait)
					}
					if !resp.State.Raw.Equal(state.Raw) {
						t.Fatal("a failed delete must keep the state")
					}
				})
			})
		}
	}
}

// runServerGroupMemberOperation runs the Create, Update, or Delete that sends
// method, and returns its diagnostics.
func runServerGroupMemberOperation(t *testing.T, r *ServerGroupMemberResource, method string) diag.Diagnostics {
	t.Helper()
	switch method {
	case http.MethodPost:
		var resp resource.CreateResponse
		resp.State = tfsdk.State{Schema: serverGroupMemberSchema(t)}
		r.Create(context.Background(), resource.CreateRequest{Plan: serverGroupMemberPlan(t, serverGroupMemberConfiguredModel())}, &resp)
		return resp.Diagnostics
	case http.MethodPatch:
		plan := serverGroupMemberConfiguredModel()
		plan.Weight = types.Int64Value(5)
		var resp resource.UpdateResponse
		resp.State = tfsdk.State{Schema: serverGroupMemberSchema(t)}
		r.Update(context.Background(), resource.UpdateRequest{
			Plan:  serverGroupMemberPlan(t, plan),
			State: serverGroupMemberState(t, serverGroupMemberConfiguredModel()),
		}, &resp)
		return resp.Diagnostics
	default:
		var resp resource.DeleteResponse
		r.Delete(context.Background(), resource.DeleteRequest{State: serverGroupMemberState(t, serverGroupMemberConfiguredModel())}, &resp)
		return resp.Diagnostics
	}
}

// memberResourceBusyBody is the refusal that the backend answers with HTTP 409
// while another operation holds the claim of the Member or of a parent.
const memberResourceBusyBody = `{"errors":[{"location":"","code":"resource_busy","message":"load balancer is busy, please try again later."}]}`

// memberAnswer is the HTTP answer to one mutation.
type memberAnswer struct {
	status int
	body   string
}

// memberTestTransport answers the Member requests of one Create, Update, or
// Delete that sends method. The nth mutation gets answers[n-1], and every
// later one succeeds. Before a successful DELETE, the nth Member GET reports
// memberStatuses as memberTestStatus does, and DELETED stands for a Member
// that is gone. After a successful DELETE, or with gone set, a Member GET
// answers 404.
type memberTestTransport struct {
	t              *testing.T
	method         string
	answers        []memberAnswer
	memberStatuses []loadbalancersdk.LoadBalancingProvisioningStatus
	gone           bool
	parents        *memberTestParents

	memberGets int
	mutations  int
	// mutationAt records the time of each mutation, and idleGetsAt the Load
	// Balancer GETs of the idle waits before it.
	mutationAt []time.Time
	idleGetsAt []int
}

func (m *memberTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	m.t.Helper()
	member := sampleServerGroupMemberDetail(memberTestID, "web-server-1", "192.168.1.10", 80, 1, false)
	switch req.Method {
	case m.method:
		m.mutations++
		m.mutationAt = append(m.mutationAt, time.Now())
		m.idleGetsAt = append(m.idleGetsAt, m.parents.loadBalancerGets)
		if m.mutations <= len(m.answers) {
			answer := m.answers[m.mutations-1]
			return serverGroupMemberHTTPResponse(req, answer.status, answer.body), nil
		}
		switch m.method {
		case http.MethodPost:
			return serverGroupMemberHTTPResponse(req, http.StatusCreated, sampleServerGroupMemberDetailJSON(m.t, member)), nil
		case http.MethodPatch:
			return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(m.t, member)), nil
		default:
			m.gone = true
			return serverGroupMemberHTTPResponse(req, http.StatusAccepted, ""), nil
		}
	case http.MethodGet:
		if m.gone {
			return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		m.memberGets++
		member.ProvisioningStatus = memberTestStatus(m.memberStatuses, m.memberGets)
		if member.ProvisioningStatus == loadbalancersdk.LoadBalancingProvisioningStatusDELETED {
			return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), nil
		}
		return serverGroupMemberHTTPResponse(req, http.StatusOK, sampleServerGroupMemberDetailJSON(m.t, member)), nil
	}
	m.t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
	return nil, fmt.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
}

// newMemberTestTransport returns a resource whose Member requests go to the
// returned transport and whose parent requests go to parents.
func newMemberTestTransport(t *testing.T, method string, parents *memberTestParents) (*ServerGroupMemberResource, *memberTestTransport) {
	t.Helper()
	transport := &memberTestTransport{t: t, method: method, parents: parents}
	return newServerGroupMemberTestResourceWithParents(t, parents, transport), transport
}

// memberOperations lists the mutations of a Member. subject is the object
// that the error of a busy refusal names.
var memberOperations = []struct {
	name    string
	method  string
	summary string
	subject string
}{
	{"create", http.MethodPost, "Error creating server group member", "the creation of a member in server group " + memberTestServerGroupID.String()},
	{"update", http.MethodPatch, "Error updating server group member", "the update of server group member " + memberTestID.String()},
	{"delete", http.MethodDelete, "Error deleting server group member", "the deletion of server group member " + memberTestID.String()},
}

// Each mutation claims the Member and its parents up to the Load Balancer,
// and the backend refuses it while one of them is pending. The idle wait
// polls at the production interval until each claimed object is idle.
func TestServerGroupMemberWaitsForIdleBeforeRequest(t *testing.T) {
	t.Parallel()

	pending := []loadbalancersdk.LoadBalancingProvisioningStatus{
		loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE,
		loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
	}
	for _, op := range memberOperations {
		for _, object := range []string{"member", "server group", "load balancer"} {
			if op.method == http.MethodPost && object == "member" {
				continue
			}
			t.Run(op.name+" "+object, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					parents := &memberTestParents{}
					r, transport := newMemberTestTransport(t, op.method, parents)
					switch object {
					case "member":
						transport.memberStatuses = pending
					case "server group":
						parents.serverGroup = pending
					default:
						parents.loadBalancer = pending
					}
					start := time.Now()
					if diags := runServerGroupMemberOperation(t, r, op.method); diags.HasError() {
						t.Fatalf("unexpected diagnostics: %v", diags)
					}
					if transport.mutations != 1 {
						t.Fatalf("mutations=%d, want 1", transport.mutations)
					}
					if waited := transport.mutationAt[0].Sub(start); waited != serverGroupMemberReadyPollInterval {
						t.Fatalf("mutation sent after %s, want one poll interval %s", waited, serverGroupMemberReadyPollInterval)
					}
				})
			})
		}
	}
}

// The backend refuses a mutation while a claimed object is in ERROR or
// UNKNOWN, and neither state clears by itself, so the idle wait fails at once
// without a request. A Delete is the exception for the Member itself: the
// idle wait of a Delete counts ERROR and UNKNOWN of the Member as idle.
func TestServerGroupMemberIdleWaitStopsOnFailedObjects(t *testing.T) {
	t.Parallel()

	for _, op := range memberOperations {
		for _, object := range []string{"member", "server group", "load balancer"} {
			for _, status := range []loadbalancersdk.LoadBalancingProvisioningStatus{
				loadbalancersdk.LoadBalancingProvisioningStatusERROR,
				loadbalancersdk.LoadBalancingProvisioningStatusUNKNOWN,
			} {
				if op.method == http.MethodPost && object == "member" {
					continue
				}
				t.Run(fmt.Sprintf("%s %s %s", op.name, object, status), func(t *testing.T) {
					t.Parallel()
					synctest.Test(t, func(t *testing.T) {
						parents := &memberTestParents{}
						r, transport := newMemberTestTransport(t, op.method, parents)
						statuses := []loadbalancersdk.LoadBalancingProvisioningStatus{status}
						switch object {
						case "member":
							transport.memberStatuses = statuses
						case "server group":
							parents.serverGroup = statuses
						default:
							parents.loadBalancer = statuses
						}
						start := time.Now()
						diags := runServerGroupMemberOperation(t, r, op.method)
						if op.method == http.MethodDelete && object == "member" {
							if diags.HasError() || transport.mutations != 1 {
								t.Fatalf("diagnostics=%v mutations=%d, want the Member deleted", diags, transport.mutations)
							}
							return
						}
						if !diags.HasError() || diags[0].Summary() != op.summary {
							t.Fatalf("diagnostics=%v, want %q", diags, op.summary)
						}
						if detail := diags[0].Detail(); !strings.Contains(detail, string(status)) {
							t.Fatalf("diagnostic detail=%q, want status %s", detail, status)
						}
						if transport.mutations != 0 || time.Since(start) != 0 {
							t.Fatalf("mutations=%d elapsed=%s, want no request and no wait", transport.mutations, time.Since(start))
						}
					})
				})
			}
		}
	}
}

// A busy refusal after the idle wait means that another client holds the
// claim, so the mutation returns it at once. The error names the refused
// operation and its object, and keeps the refusal of the backend.
func TestServerGroupMemberReturnsABusyRefusal(t *testing.T) {
	t.Parallel()

	for _, op := range memberOperations {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				parents := &memberTestParents{loadBalancer: []loadbalancersdk.LoadBalancingProvisioningStatus{
					loadbalancersdk.LoadBalancingProvisioningStatusPENDINGUPDATE,
					loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
				}}
				r, transport := newMemberTestTransport(t, op.method, parents)
				transport.answers = []memberAnswer{{http.StatusConflict, memberResourceBusyBody}}
				start := time.Now()
				diags := runServerGroupMemberOperation(t, r, op.method)
				if !diags.HasError() || diags[0].Summary() != op.summary {
					t.Fatalf("diagnostics=%v, want %q", diags, op.summary)
				}
				detail := diags[0].Detail()
				for _, want := range []string{op.subject + " was refused as busy", "load balancer is busy"} {
					if !strings.Contains(detail, want) {
						t.Fatalf("diagnostic detail=%q, want %q", detail, want)
					}
				}
				if transport.mutations != 1 {
					t.Fatalf("mutations=%d, want exactly one request", transport.mutations)
				}
				if waited := transport.mutationAt[0].Sub(start); waited != serverGroupMemberReadyPollInterval {
					t.Fatalf("mutation sent after %s, want it after the idle wait of %s", waited, serverGroupMemberReadyPollInterval)
				}
				if transport.idleGetsAt[0] != 2 || parents.loadBalancerGets != 2 {
					t.Fatalf("load balancer GETs before the mutation=%d and in total=%d, want 2 and 2", transport.idleGetsAt[0], parents.loadBalancerGets)
				}
				if elapsed := time.Since(start); elapsed != serverGroupMemberReadyPollInterval {
					t.Fatalf("operation took %s, want no wait after the refusal", elapsed)
				}
			})
		})
	}
}

// Only HTTP 409 with code resource_busy is a busy refusal. Every other answer,
// a 409 with code conflict included, returns unchanged after the one request.
func TestServerGroupMemberReturnsOtherRefusalsAtOnce(t *testing.T) {
	t.Parallel()

	refusals := []struct {
		name   string
		answer memberAnswer
		want   string
	}{
		{"409 conflict", memberAnswer{http.StatusConflict, `{"errors":[{"location":"","code":"conflict","message":"The last operation on load balancer failed."}]}`}, "last operation on load balancer failed"},
		{"400 resource_busy", memberAnswer{http.StatusBadRequest, memberResourceBusyBody}, "load balancer is busy"},
		{"500 resource_busy", memberAnswer{http.StatusInternalServerError, memberResourceBusyBody}, "load balancer is busy"},
	}
	for _, op := range memberOperations {
		for _, refusal := range refusals {
			t.Run(op.name+" "+refusal.name, func(t *testing.T) {
				t.Parallel()
				synctest.Test(t, func(t *testing.T) {
					r, transport := newMemberTestTransport(t, op.method, &memberTestParents{})
					transport.answers = []memberAnswer{refusal.answer}
					start := time.Now()
					diags := runServerGroupMemberOperation(t, r, op.method)
					if !diags.HasError() || diags[0].Summary() != op.summary {
						t.Fatalf("diagnostics=%v, want %q", diags, op.summary)
					}
					detail := diags[0].Detail()
					if !strings.Contains(detail, refusal.want) {
						t.Fatalf("diagnostic detail=%q, want %q", detail, refusal.want)
					}
					if strings.Contains(detail, "refused as busy") {
						t.Fatalf("diagnostic detail=%q, want no busy refusal", detail)
					}
					if transport.mutations != 1 || time.Since(start) != 0 {
						t.Fatalf("mutations=%d elapsed=%s, want the refusal returned on the first attempt", transport.mutations, time.Since(start))
					}
				})
			})
		}
	}
}

// A Delete that finds the Member gone in the idle wait is done, and sends no
// DELETE.
func TestServerGroupMemberDeleteFindsTheMemberGone(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		r, transport := newMemberTestTransport(t, http.MethodDelete, &memberTestParents{})
		transport.gone = true
		if diags := runServerGroupMemberOperation(t, r, http.MethodDelete); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if transport.mutations != 0 {
			t.Fatalf("DELETE requests=%d, want 0", transport.mutations)
		}
	})
}

// The owner search finds no Load Balancer once the Server Group is gone. The
// backend deletes the Members of a deleted Server Group, so a Delete is done
// when the Member is gone too, and every other operation fails.
func TestServerGroupMemberWithoutOwnerLoadBalancer(t *testing.T) {
	t.Parallel()

	for _, op := range memberOperations {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			r, transport := newMemberTestTransport(t, op.method, &memberTestParents{orphan: true})
			transport.gone = true
			diags := runServerGroupMemberOperation(t, r, op.method)
			if transport.mutations != 0 {
				t.Fatalf("mutations=%d, want none", transport.mutations)
			}
			if op.method == http.MethodDelete {
				if diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				return
			}
			if !diags.HasError() || !strings.Contains(diags[0].Detail(), "resolve the load balancer of server group") {
				t.Fatalf("diagnostics=%v, want the owner search error", diags)
			}
		})
	}
}

// Sibling children of one Load Balancer refuse each other. A mutation therefore
// holds the claims lock of the root Load Balancer, and waits while another
// operation of this provider holds it.
func TestServerGroupMemberOperationsHoldTheLoadBalancerLock(t *testing.T) {
	t.Parallel()

	for _, op := range memberOperations {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r, transport := newMemberTestTransport(t, op.method, &memberTestParents{})
				unlock, err := r.claims.Lock(context.Background(), memberTestLoadBalancerID.String())
				if err != nil {
					t.Fatalf("lock: %v", err)
				}
				done := make(chan diag.Diagnostics)
				go func() { done <- runServerGroupMemberOperation(t, r, op.method) }()
				synctest.Wait()
				if transport.mutations != 0 {
					t.Fatalf("mutations=%d while another operation holds the lock, want 0", transport.mutations)
				}
				unlock()
				if diags := <-done; diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				if transport.mutations != 1 {
					t.Fatalf("mutations=%d, want 1 after the lock is released", transport.mutations)
				}
				relock, err := r.claims.Lock(context.Background(), memberTestLoadBalancerID.String())
				if err != nil {
					t.Fatalf("operation kept the lock: %v", err)
				}
				relock()
			})
		})
	}
}

// Only the GET of the Member proves that the Member is gone. A Server Group
// that answers 404 while the Member still answers fails the Delete instead of
// completing it, so the Member stays in state.
func TestServerGroupMemberMissingParentFailsTheIdleWait(t *testing.T) {
	t.Parallel()

	for _, op := range memberOperations {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r, transport := newMemberTestTransport(t, op.method, &memberTestParents{serverGroupMissing: true})
				start := time.Now()
				diags := runServerGroupMemberOperation(t, r, op.method)
				if !diags.HasError() || diags[0].Summary() != op.summary {
					t.Fatalf("diagnostics=%v, want %q", diags, op.summary)
				}
				if detail := diags[0].Detail(); !strings.Contains(detail, "server group "+memberTestServerGroupID.String()) {
					t.Fatalf("diagnostic detail=%q, want the missing server group", detail)
				}
				if transport.mutations != 0 || time.Since(start) != 0 {
					t.Fatalf("mutations=%d elapsed=%s, want no request and no wait", transport.mutations, time.Since(start))
				}
			})
		})
	}
}
