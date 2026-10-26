package network

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
)

var (
	privateIPDataSourceTestID        = core.UUID{0x11}
	privateIPDataSourceTestSubnetID  = core.UUID{0x22}
	privateIPDataSourceTestVPCID     = core.UUID{0x33}
	privateIPDataSourceTestProjectID = core.UUID{0x44}
	privateIPDataSourceTestServerID  = core.UUID{0x55}
	privateIPDataSourceTestVIPID     = core.UUID{0x66}
)

func TestPrivateIPDataSourceModelMatchesSchema(t *testing.T) {
	t.Parallel()

	state := tfsdk.State{Schema: privateIPDataSourceSchema(t)}
	model := nullPrivateIPDataSourceModel()
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("data source model does not match schema: %v", diags)
	}
}

func TestPrivateIPDataSourceConstructorMetadataAndConfigure(t *testing.T) {
	t.Parallel()

	d := NewPrivateIPDataSource()
	if d == nil {
		t.Fatal("expected non-nil data source")
	}
	var metadataResponse datasource.MetadataResponse
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "viettelcloud"}, &metadataResponse)
	if metadataResponse.TypeName != "viettelcloud_private_ip" {
		t.Fatalf("expected viettelcloud_private_ip metadata, got %q", metadataResponse.TypeName)
	}

	dataSource := &PrivateIPDataSource{}
	var nilResponse datasource.ConfigureResponse
	dataSource.Configure(context.Background(), datasource.ConfigureRequest{}, &nilResponse)
	if nilResponse.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics for nil provider data: %v", nilResponse.Diagnostics)
	}
	var invalidResponse datasource.ConfigureResponse
	dataSource.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "invalid"}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("expected invalid provider data diagnostic")
	}

	networkClient, err := networksdk.NewClient("http://localhost", networksdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	projectClient, err := projectsdk.NewClient("http://localhost", projectsdk.WithRetry(core.NoRetry()))
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	var validResponse datasource.ConfigureResponse
	dataSource.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: &providerdata.Configured{
		Network: networkClient, Project: projectClient, ProjectID: privateIPDataSourceTestProjectID,
	}}, &validResponse)
	if validResponse.Diagnostics.HasError() || dataSource.client != networkClient || dataSource.project != projectClient ||
		dataSource.projectID != privateIPDataSourceTestProjectID {
		t.Fatalf("unexpected configured data source: %#v diagnostics=%v", dataSource, validResponse.Diagnostics)
	}
}

func TestPrivateIPDataSourceFilterSchema(t *testing.T) {
	t.Parallel()

	dsSchema := privateIPDataSourceSchema(t)
	for _, name := range []string{
		"id", "ip_address", "subnet_id", "subnet_name", "subnet_cidr",
		"vpc_id", "vpc_name", "region",
	} {
		attribute, ok := dsSchema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || !attribute.Optional || !attribute.Computed || attribute.Required {
			t.Fatalf("expected %s to be optional and computed, got %#v", name, dsSchema.Attributes[name])
		}
	}
	vpcCIDR, ok := dsSchema.Attributes["vpc_cidr"].(datasourceschema.StringAttribute)
	if !ok || !vpcCIDR.Optional || vpcCIDR.Computed || vpcCIDR.Required {
		t.Fatalf("expected vpc_cidr to be an optional-only lookup filter, got %#v", dsSchema.Attributes["vpc_cidr"])
	}
	for _, name := range []string{
		"description", "mac_address", "server_id", "server_name", "device_owner", "display_name", "created_at", "updated_at",
	} {
		attribute, ok := dsSchema.Attributes[name].(datasourceschema.StringAttribute)
		if !ok || attribute.Optional || !attribute.Computed || attribute.Required {
			t.Fatalf("expected %s to be computed only, got %#v", name, dsSchema.Attributes[name])
		}
	}
	portSecurity, ok := dsSchema.Attributes["port_security"].(datasourceschema.BoolAttribute)
	if !ok || portSecurity.Optional || !portSecurity.Computed || portSecurity.Required {
		t.Fatalf("expected port_security to be computed only, got %#v", dsSchema.Attributes["port_security"])
	}
	for _, name := range []string{"allowed_cidrs", "allowed_vip_ids"} {
		attribute, ok := dsSchema.Attributes[name].(datasourceschema.SetAttribute)
		if !ok || attribute.Optional || !attribute.Computed || attribute.Required || attribute.ElementType != types.StringType {
			t.Fatalf("expected %s to be a computed string set, got %#v", name, dsSchema.Attributes[name])
		}
	}
	attachedVIPs, ok := dsSchema.Attributes["attached_vips"].(datasourceschema.SetNestedAttribute)
	if !ok || attachedVIPs.Optional || !attachedVIPs.Computed || attachedVIPs.Required ||
		len(attachedVIPs.NestedObject.Attributes) != len(privateIPAttachedVIPAttributeTypes) {
		t.Fatalf("expected attached_vips to be a computed nested set, got %#v", dsSchema.Attributes["attached_vips"])
	}
}

func TestPrivateIPDataSourceRequiresIDOrIPAddress(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]PrivateIPDataSourceModel{
		"empty": nullPrivateIPDataSourceModel(),
		"unknown ID/IP": {
			PrivateIPModel: PrivateIPModel{
				ID:        types.StringUnknown(),
				IPAddress: types.StringUnknown(),
			},
		},
		"scope only":  privateIPDataSourceModelWithString("vpc_name", "production"),
		"subnet only": privateIPDataSourceModelWithString("subnet_cidr", "10.0.1.0/24"),
		"region only": privateIPDataSourceModelWithString("region", "vn-central-1"),
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := (&PrivateIPDataSource{}).getPrivateIP(context.Background(), config)
			if !diags.HasError() || diags.Errors()[0].Summary() != "Missing private IP lookup criteria" {
				t.Fatalf("expected missing criteria diagnostic, got %v", diags)
			}
		})
	}
}

func TestPrivateIPDataSourceReadRejectsMalformedConfig(t *testing.T) {
	t.Parallel()

	dsSchema := privateIPDataSourceSchema(t)
	response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	(&PrivateIPDataSource{}).Read(context.Background(), datasource.ReadRequest{
		Config: tfsdk.Config{Raw: tftypes.NewValue(tftypes.String, "invalid"), Schema: dsSchema},
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected malformed configuration diagnostic")
	}
}

func TestPrivateIPDataSourceRejectsWhitespaceFilters(t *testing.T) {
	t.Parallel()

	for _, field := range []string{
		"ip_address", "subnet_name", "subnet_cidr", "vpc_name", "vpc_cidr", "region",
	} {
		t.Run(field, func(t *testing.T) {
			config := privateIPDataSourceModelWithString("ip_address", "10.0.1.10")
			setPrivateIPDataSourceString(&config, field, " ")
			_, diags := (&PrivateIPDataSource{}).getPrivateIP(context.Background(), config)
			if !diags.HasError() {
				t.Fatalf("expected whitespace %s diagnostic", field)
			}
		})
	}
}

func TestPrivateIPDataSourceRejectsInvalidIDs(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"id", "subnet_id", "vpc_id"} {
		t.Run(field, func(t *testing.T) {
			config := privateIPDataSourceModelWithString("ip_address", "10.0.1.10")
			setPrivateIPDataSourceString(&config, field, "invalid-uuid")
			_, diags := (&PrivateIPDataSource{}).getPrivateIP(context.Background(), config)
			if !diags.HasError() {
				t.Fatalf("expected invalid %s diagnostic", field)
			}
		})
	}
}

func TestPrivateIPDataSourceReadByDirectID(t *testing.T) {
	t.Parallel()

	privateIP := mockPrivateIPDataSource("existing address")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v2/network/private-ips/" + privateIPDataSourceTestID.String() + "/":
			writePrivateIPDataSourceResponse(t, w, privateIP)
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.String())
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	d := privateIPDataSourceForServer(t, server)
	config := nullPrivateIPDataSourceModel()
	configuredID := strings.ToUpper(privateIPDataSourceTestID.String())
	config.ID = types.StringValue(configuredID)
	response := readPrivateIPDataSource(t, d, config)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source diagnostics: %v", response.Diagnostics)
	}

	var state PrivateIPDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get data source state: %v", diags)
	}
	if state.ID.ValueString() != configuredID || state.IPAddress.ValueString() != "10.0.1.10" ||
		state.SubnetCIDR.ValueString() != "10.0.1.0/24" || !state.VPCCIDR.IsNull() ||
		state.ServerID.ValueString() != privateIPDataSourceTestServerID.String() || len(state.AttachedVIPs.Elements()) != 1 {
		t.Fatalf("unexpected direct-ID state: %#v", state)
	}
}

func TestPrivateIPDataSourceReadDirectIDErrors(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		status  int
		summary string
	}{
		"not found": {status: http.StatusNotFound, summary: "Private IP not found"},
		"API error": {status: http.StatusInternalServerError, summary: "Error reading private IP"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"detail":"request failed"}`, test.status)
			}))
			defer server.Close()

			config := privateIPDataSourceModelWithString("id", privateIPDataSourceTestID.String())
			response := readPrivateIPDataSource(t, privateIPDataSourceForServer(t, server), config)
			if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != test.summary {
				t.Fatalf("expected %s diagnostic, got %v", test.summary, response.Diagnostics)
			}
		})
	}
}

func TestPrivateIPDataSourceDirectIDRejectsInvalidUUID(t *testing.T) {
	t.Parallel()

	_, diags := (&PrivateIPDataSource{}).getPrivateIPByID(context.Background(), types.StringValue("invalid-uuid"))
	if !diags.HasError() || diags.Errors()[0].Summary() != "Invalid UUID" {
		t.Fatalf("expected invalid direct-ID diagnostic, got %v", diags)
	}
}

func TestPrivateIPDataSourceParentIDFastPaths(t *testing.T) {
	t.Parallel()

	d := &PrivateIPDataSource{}
	vpcConfig := nullPrivateIPDataSourceModel()
	vpcConfig.VPCID = types.StringValue(strings.ToUpper(privateIPDataSourceTestVPCID.String()))
	vpcID, vpcDiags := d.resolvePrivateIPVPC(context.Background(), vpcConfig)
	if vpcDiags.HasError() || vpcID == nil || *vpcID != privateIPDataSourceTestVPCID {
		t.Fatalf("unexpected direct VPC resolution: id=%v diagnostics=%v", vpcID, vpcDiags)
	}

	subnetConfig := nullPrivateIPDataSourceModel()
	subnetConfig.SubnetID = types.StringValue(strings.ToUpper(privateIPDataSourceTestSubnetID.String()))
	subnetID, subnetDiags := d.resolvePrivateIPSubnet(context.Background(), subnetConfig, vpcID)
	if subnetDiags.HasError() || subnetID == nil || *subnetID != privateIPDataSourceTestSubnetID {
		t.Fatalf("unexpected direct subnet resolution: id=%v diagnostics=%v", subnetID, subnetDiags)
	}
}

func TestPrivateIPDataSourceRegionResolutionError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encodePrivateIPDataSourceResponse(t, w, projectsdk.PagedProjectRegionSchema{Count: 0})
	}))
	defer server.Close()

	config := nullPrivateIPDataSourceModel()
	config.VPCName = types.StringValue("production")
	config.Region = types.StringValue("missing-region")
	_, diags := privateIPDataSourceForServer(t, server).resolvePrivateIPVPC(context.Background(), config)
	if !diags.HasError() || diags.Errors()[0].Summary() != "Error resolving region" {
		t.Fatalf("expected region resolution diagnostic, got %v", diags)
	}
}

func TestPrivateIPDataSourceReadResolvesHierarchy(t *testing.T) {
	t.Parallel()

	regionID := core.UUID{0x77}
	privateIP := mockPrivateIPDataSource("existing address")
	vpc := privateIPTestVPC("10.0.0.0/16")
	subnet := privateIPTestSubnet()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v2/projects/" + privateIPDataSourceTestProjectID.String() + "/regions/":
			if req.URL.Query().Get("name") != "vn-central-1" {
				t.Errorf("unexpected region query: %v", req.URL.Query())
			}
			encodePrivateIPDataSourceResponse(t, w, projectsdk.PagedProjectRegionSchema{
				Count: 1,
				Results: []projectsdk.ProjectRegionSchema{{
					Region: projectsdk.NestedRegionSchema{Id: regionID, Name: "vn-central-1"},
				}},
			})
		case "/v2/network/vpcs/":
			query := req.URL.Query()
			if query.Get("name") != "production" || query.Get("region_id") != regionID.String() {
				t.Errorf("unexpected VPC query: %v", query)
			}
			encodePrivateIPDataSourceResponse(t, w, networksdk.PagedVPCSchema{Count: 1, Results: []networksdk.VPCSchema{*vpc}})
		case "/v2/network/subnets/":
			query := req.URL.Query()
			if query.Get("cidr") != "10.0.1.0/24" || query.Get("vpc_id") != privateIPDataSourceTestVPCID.String() ||
				query.Get("region") != "vn-central-1" {
				t.Errorf("unexpected subnet query: %v", query)
			}
			encodePrivateIPDataSourceResponse(t, w, networksdk.PagedSubnetSchema{Count: 1, Results: []networksdk.SubnetSchema{*subnet}})
		case "/v2/network/private-ips/":
			query := req.URL.Query()
			if query.Get("ip_address") != "10.0.1.10" || query.Get("subnet_id") != privateIPDataSourceTestSubnetID.String() ||
				query.Get("vpc_id") != privateIPDataSourceTestVPCID.String() || query.Get("region") != "vn-central-1" {
				t.Errorf("unexpected private IP query: %v", query)
			}
			encodePrivateIPDataSourceResponse(t, w, networksdk.PagedPrivateIPSchema{
				Count: 1, Results: []networksdk.PrivateIPSchema{*privateIP},
			})
		default:
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.String())
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	d := privateIPDataSourceForServer(t, server)
	config := nullPrivateIPDataSourceModel()
	config.IPAddress = types.StringValue(" 10.0.1.10 ")
	config.SubnetName = types.StringValue(" application ")
	config.SubnetCIDR = types.StringValue(" 10.0.1.0/24 ")
	config.VPCName = types.StringValue(" production ")
	config.VPCCIDR = types.StringValue(" 10.0.0.0/16 ")
	config.Region = types.StringValue(" vn-central-1 ")
	response := readPrivateIPDataSource(t, d, config)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source diagnostics: %v", response.Diagnostics)
	}
	if len(requests) != 4 {
		t.Fatalf("expected four hierarchical requests, got %v", requests)
	}

	var state PrivateIPDataSourceModel
	if diags := response.State.Get(context.Background(), &state); diags.HasError() {
		t.Fatalf("get data source state: %v", diags)
	}
	if state.IPAddress.ValueString() != config.IPAddress.ValueString() ||
		state.SubnetName.ValueString() != config.SubnetName.ValueString() ||
		state.SubnetCIDR.ValueString() != config.SubnetCIDR.ValueString() ||
		state.VPCName.ValueString() != config.VPCName.ValueString() ||
		state.VPCCIDR.ValueString() != config.VPCCIDR.ValueString() ||
		state.Region.ValueString() != config.Region.ValueString() {
		t.Fatalf("expected configured representations to be preserved, got %#v", state)
	}
}

func TestPrivateIPDataSourceFilterCardinalityErrors(t *testing.T) {
	t.Parallel()

	for name, results := range map[string][]networksdk.PrivateIPSchema{
		"zero": nil,
		"multiple": {
			*mockPrivateIPDataSource("first"),
			func() networksdk.PrivateIPSchema {
				privateIP := *mockPrivateIPDataSource("second")
				privateIP.Id = core.UUID{0x99}
				return privateIP
			}(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				encodePrivateIPDataSourceResponse(t, w, networksdk.PagedPrivateIPSchema{Count: len(results), Results: results})
			}))
			defer server.Close()

			config := privateIPDataSourceModelWithString("ip_address", "10.0.1.10")
			response := readPrivateIPDataSource(t, privateIPDataSourceForServer(t, server), config)
			if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Error resolving private IP" {
				t.Fatalf("expected private IP cardinality diagnostic, got %v", response.Diagnostics)
			}
		})
	}
}

func TestPrivateIPDataSourceParentCardinalityErrors(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		config  PrivateIPDataSourceModel
		results any
		summary string
	}{
		"VPC": {
			config: func() PrivateIPDataSourceModel {
				model := privateIPDataSourceModelWithString("ip_address", "10.0.1.10")
				model.VPCName = types.StringValue("duplicate")
				return model
			}(),
			results: networksdk.PagedVPCSchema{Count: 0},
			summary: "Error resolving VPC",
		},
		"subnet": {
			config: func() PrivateIPDataSourceModel {
				model := privateIPDataSourceModelWithString("ip_address", "10.0.1.10")
				model.SubnetCIDR = types.StringValue("10.0.1.0/24")
				return model
			}(),
			results: networksdk.PagedSubnetSchema{Count: 0},
			summary: "Error resolving subnet",
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				encodePrivateIPDataSourceResponse(t, w, test.results)
			}))
			defer server.Close()

			response := readPrivateIPDataSource(t, privateIPDataSourceForServer(t, server), test.config)
			if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != test.summary {
				t.Fatalf("expected %s diagnostic, got %v", test.summary, response.Diagnostics)
			}
		})
	}
}

func TestPreserveConfiguredPrivateIPValuesUsesChangedBackendValues(t *testing.T) {
	t.Parallel()

	privateIP := mockPrivateIPDataSource("existing")
	config := privateIPDataSourceModelWithString("ip_address", "10.9.9.9")
	config.SubnetName = types.StringValue("other-subnet")
	config.SubnetCIDR = types.StringValue("10.9.0.0/24")
	config.VPCName = types.StringValue("other-vpc")
	config.Region = types.StringValue("other-region")
	state := nullPrivateIPDataSourceModel()
	populatePrivateIPDataSourceState(privateIP, &state)
	preserveConfiguredPrivateIPValues(privateIP, config, &state)

	if state.IPAddress.ValueString() != *privateIP.IpAddress || state.SubnetName.ValueString() != privateIP.Subnet.Name ||
		state.SubnetCIDR.ValueString() != privateIP.Subnet.Cidr || state.VPCName.ValueString() != privateIP.Vpc.Name ||
		state.Region.ValueString() != privateIP.Region.Name {
		t.Fatalf("expected backend values when criteria differ, got %#v", state)
	}

	unchanged := privateIPDataSourceModelWithString("id", "unchanged")
	preserveConfiguredPrivateIPValues(nil, PrivateIPDataSourceModel{}, &unchanged)
	if unchanged.ID.ValueString() != "unchanged" {
		t.Fatalf("expected nil inputs to leave state unchanged, got %#v", unchanged)
	}

	matching := nullPrivateIPDataSourceModel()
	matching.SubnetID = types.StringValue(strings.ToUpper(privateIP.Subnet.Id.String()))
	matching.VPCID = types.StringValue(strings.ToUpper(privateIP.Vpc.Id.String()))
	populatePrivateIPDataSourceState(privateIP, &state)
	preserveConfiguredPrivateIPValues(privateIP, matching, &state)
	if state.SubnetID.ValueString() != matching.SubnetID.ValueString() || state.VPCID.ValueString() != matching.VPCID.ValueString() {
		t.Fatalf("expected equivalent parent UUID representations to be preserved, got %#v", state)
	}
}

func TestPopulatePrivateIPDataSourceStateHandlesAbsentRelationships(t *testing.T) {
	t.Parallel()

	unchangedModel := PrivateIPModel{ID: types.StringValue("unchanged")}
	populatePrivateIPModel(nil, &unchangedModel)
	if unchangedModel.ID.ValueString() != "unchanged" {
		t.Fatalf("expected nil private IP to leave shared model unchanged, got %#v", unchangedModel)
	}

	unchanged := privateIPDataSourceModelWithString("id", "unchanged")
	populatePrivateIPDataSourceState(nil, &unchanged)
	if unchanged.ID.ValueString() != "unchanged" {
		t.Fatalf("expected nil private IP to leave state unchanged, got %#v", unchanged)
	}

	privateIP := mockPrivateIPDataSource("without optional relationships")
	privateIP.Server = nil
	privateIP.AllowedCidrs = nil
	privateIP.AttachedVips = nil
	state := nullPrivateIPDataSourceModel()
	populatePrivateIPDataSourceState(privateIP, &state)
	if !state.ServerID.IsNull() || !state.ServerName.IsNull() || !state.AllowedCIDRs.IsNull() ||
		!state.AllowedVIPIDs.IsNull() || !state.AttachedVIPs.IsNull() {
		t.Fatalf("expected absent optional relationships to map to null, got %#v", state)
	}
}

func privateIPDataSourceSchema(t *testing.T) datasourceschema.Schema {
	t.Helper()
	var response datasource.SchemaResponse
	(&PrivateIPDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func nullPrivateIPDataSourceModel() PrivateIPDataSourceModel {
	return PrivateIPDataSourceModel{
		PrivateIPModel: PrivateIPModel{
			AllowedCIDRs: types.SetNull(types.StringType),
			AttachedVIPs: types.SetNull(types.ObjectType{AttrTypes: privateIPAttachedVIPAttributeTypes}),
		},
		AllowedVIPIDs: types.SetNull(types.StringType),
	}
}

func privateIPDataSourceModelWithString(field, value string) PrivateIPDataSourceModel {
	model := nullPrivateIPDataSourceModel()
	setPrivateIPDataSourceString(&model, field, value)
	return model
}

func setPrivateIPDataSourceString(model *PrivateIPDataSourceModel, field, value string) {
	attribute := types.StringValue(value)
	switch field {
	case "id":
		model.ID = attribute
	case "ip_address":
		model.IPAddress = attribute
	case "subnet_id":
		model.SubnetID = attribute
	case "subnet_name":
		model.SubnetName = attribute
	case "subnet_cidr":
		model.SubnetCIDR = attribute
	case "vpc_id":
		model.VPCID = attribute
	case "vpc_name":
		model.VPCName = attribute
	case "vpc_cidr":
		model.VPCCIDR = attribute
	case "region":
		model.Region = attribute
	default:
		panic("unsupported private IP data source field: " + field)
	}
}

func readPrivateIPDataSource(
	t *testing.T,
	d *PrivateIPDataSource,
	config PrivateIPDataSourceModel,
) datasource.ReadResponse {
	t.Helper()
	dsSchema := privateIPDataSourceSchema(t)
	configState := tfsdk.State{Schema: dsSchema}
	if diags := configState.Set(context.Background(), &config); diags.HasError() {
		t.Fatalf("set data source config: %v", diags)
	}
	response := datasource.ReadResponse{State: tfsdk.State{Schema: dsSchema}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: tfsdk.Config{Raw: configState.Raw, Schema: dsSchema},
	}, &response)
	return response
}

func privateIPDataSourceForServer(t *testing.T, server *httptest.Server) *PrivateIPDataSource {
	t.Helper()
	networkClient, err := networksdk.NewClient(
		server.URL,
		networksdk.WithHTTPClient(server.Client()),
		networksdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create network client: %v", err)
	}
	projectClient, err := projectsdk.NewClient(
		server.URL,
		projectsdk.WithHTTPClient(server.Client()),
		projectsdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create project client: %v", err)
	}
	return &PrivateIPDataSource{client: networkClient, project: projectClient, projectID: privateIPDataSourceTestProjectID}
}

func privateIPTestVPC(cidr string) *networksdk.VPCSchema {
	vpc := mockVPC()
	vpc.Id = privateIPDataSourceTestVPCID
	vpc.Name = "production"
	vpc.Cidr = cidr
	vpc.Region = networksdk.NestedRegionSchema{Id: core.UUID{0x77}, Name: "vn-central-1"}
	return vpc
}

func privateIPTestSubnet() *networksdk.SubnetSchema {
	subnet := mockSubnet("application", "application tier")
	subnet.Id = privateIPDataSourceTestSubnetID
	subnet.Vpc = networksdk.NestedVPCSchema{Id: privateIPDataSourceTestVPCID, Name: "production", DisplayName: "production"}
	subnet.Region = networksdk.NestedRegionSchema{Id: core.UUID{0x77}, Name: "vn-central-1"}
	return subnet
}

func encodePrivateIPDataSourceResponse(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode data source response: %v", err)
	}
}

func mockPrivateIPDataSource(description string) *networksdk.PrivateIPSchema {
	ipAddress := "10.0.1.10"
	macAddress := "aa:bb:cc:dd:ee:ff"
	vipAddress := "10.0.1.100"
	allowedCIDRs := []string{"10.0.1.0/24", "10.0.2.0/24"}
	attachedVIPs := []networksdk.NestedPrivateIPAllowedVIPSchema{{
		Id:        privateIPDataSourceTestVIPID,
		IpAddress: &vipAddress,
	}}
	return &networksdk.PrivateIPSchema{
		Id:           privateIPDataSourceTestID,
		Description:  &description,
		IpAddress:    &ipAddress,
		MacAddress:   &macAddress,
		DisplayName:  ipAddress,
		DeviceOwner:  networksdk.PrivateIPDeviceOwnerUser,
		PortSecurity: true,
		AllowedCidrs: &allowedCIDRs,
		AttachedVips: &attachedVIPs,
		Subnet: networksdk.NestedSubnetSchema{
			Id:   privateIPDataSourceTestSubnetID,
			Name: "application",
			Cidr: "10.0.1.0/24",
		},
		Vpc: networksdk.NestedVPCSchema{
			Id:          privateIPDataSourceTestVPCID,
			Name:        "production",
			DisplayName: "production",
		},
		Region: networksdk.NestedRegionSchema{
			Id:          core.UUID{0x77},
			Name:        "vn-central-1",
			Description: "Central region",
		},
		Server: &networksdk.NestedServerSchema{
			Id:   privateIPDataSourceTestServerID,
			Name: "app-server",
		},
		Project: networksdk.NestedProjectSchema{
			Id:   privateIPDataSourceTestProjectID,
			Name: "integration",
			Slug: "integration",
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func writePrivateIPDataSourceResponse(t *testing.T, w http.ResponseWriter, privateIP *networksdk.PrivateIPSchema) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(privateIP); err != nil {
		t.Errorf("encode private IP response: %v", err)
	}
}
