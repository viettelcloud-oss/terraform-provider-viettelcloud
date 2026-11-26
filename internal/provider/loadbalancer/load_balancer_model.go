package loadbalancer

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/compare"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

type LoadBalancerModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	Package            types.String `tfsdk:"package"`
	Topology           types.String `tfsdk:"topology"`
	Type               types.String `tfsdk:"type"`
	Region             types.Object `tfsdk:"region"`
	RegionID           types.String `tfsdk:"region_id"`
	VPC                types.Object `tfsdk:"vpc"`
	VPCName            types.String `tfsdk:"vpc_name"`
	PrivateIP          types.Object `tfsdk:"private_ip"`
	PrivateIPID        types.String `tfsdk:"private_ip_id"`
	QoSPolicy          types.Object `tfsdk:"qos_policy"`
	QoSPolicyID        types.String `tfsdk:"qos_policy_id"`
	QoSPolicyName      types.String `tfsdk:"qos_policy_name"`
	VIPAddress         types.String `tfsdk:"vip_address"`
	PublicAddress      types.String `tfsdk:"public_address"`
	V6PublicAddress    types.String `tfsdk:"v6_public_address"`
	Status             types.String `tfsdk:"status"`
	ProvisioningStatus types.String `tfsdk:"provisioning_status"`
	AccessLogEnabled   types.Bool   `tfsdk:"access_log_enabled"`
	AccessLogStoreID   types.String `tfsdk:"access_log_store_id"`
	AccessLogStoreName types.String `tfsdk:"access_log_store_name"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
}

type LoadBalancerResourceModel struct {
	LoadBalancerModel
	PublicIPMode types.String `tfsdk:"public_ip_mode"`
}

// populateLoadBalancerModel maps the API response to the shared model.
func populateLoadBalancerModel(lb *loadbalancersdk.LoadBalancerDetailSchema, m *LoadBalancerModel) {
	if lb == nil {
		return
	}

	configuredName := m.Name
	configuredDescription := m.Description
	configuredPackage := m.Package
	configuredTopology := m.Topology
	configuredRegion := reference.Get(m.Region, reference.Name)

	m.ID = types.StringValue(lb.Id.String())
	m.Name = types.StringValue(lb.Name)
	m.Description = types.StringPointerValue(core.NullableToPtr(lb.Description))
	m.Package = types.StringValue(string(lb.Package))
	m.Topology = types.StringValue(string(lb.Topology))
	m.Type = loadBalancerTypeState(m.Type, lb.Type)
	m.Region = reference.Value(reference.Name, types.StringValue(lb.Region.Name))
	m.RegionID = types.StringValue(lb.Region.Id.String())
	m.VPCName = types.StringValue(lb.Vpc.Name)
	privateIPID := core.NullableToPtr(lb.PrivateIpId)
	if privateIPID == nil {
		m.PrivateIPID = types.StringNull()
	} else {
		m.PrivateIPID = types.StringValue(privateIPID.String())
	}
	m.VIPAddress = types.StringPointerValue(core.NullableToPtr(lb.VipAddress))
	m.PublicAddress = types.StringPointerValue(core.NullableToPtr(lb.PublicAddress))
	m.V6PublicAddress = types.StringPointerValue(core.NullableToPtr(lb.V6PublicAddress))
	m.Status = types.StringValue(string(lb.Status))
	m.ProvisioningStatus = types.StringValue(string(lb.ProvisioningStatus))
	m.AccessLogEnabled = types.BoolValue(lb.AccessLogEnabled)
	if accessLogStore := core.NullableToPtr(lb.AccessLogStore); accessLogStore == nil {
		m.AccessLogStoreID = types.StringNull()
		m.AccessLogStoreName = types.StringNull()
	} else {
		m.AccessLogStoreID = types.StringValue(accessLogStore.Id.String())
		m.AccessLogStoreName = types.StringValue(accessLogStore.Name)
	}
	m.CreatedAt = types.StringValue(lb.CreatedAt.Format(time.RFC3339))
	m.UpdatedAt = types.StringValue(lb.UpdatedAt.Format(time.RFC3339))
	qosPolicy := core.NullableToPtr(lb.QosPolicy)
	if qosPolicy == nil {
		m.QoSPolicyID = types.StringNull()
		m.QoSPolicyName = types.StringNull()
	} else {
		m.QoSPolicyID = types.StringValue(qosPolicy.Id.String())
		m.QoSPolicyName = types.StringValue(qosPolicy.Name)
	}

	if !configuredName.IsNull() && !configuredName.IsUnknown() && strings.TrimSpace(configuredName.ValueString()) == lb.Name {
		m.Name = configuredName
	}
	// The request trims description, so keep the configured spelling when it
	// differs from the backend value only by outer whitespace.
	if description := core.NullableToPtr(lb.Description); description != nil {
		m.Description = compare.PreservedString(configuredDescription, *description)
	}
	if !configuredPackage.IsNull() && !configuredPackage.IsUnknown() && strings.TrimSpace(configuredPackage.ValueString()) == string(lb.Package) {
		m.Package = configuredPackage
	}
	if !configuredTopology.IsNull() && !configuredTopology.IsUnknown() && strings.TrimSpace(configuredTopology.ValueString()) == string(lb.Topology) {
		m.Topology = configuredTopology
	}
	if !configuredRegion.IsNull() && !configuredRegion.IsUnknown() && strings.TrimSpace(configuredRegion.ValueString()) == lb.Region.Name {
		m.Region = reference.Value(reference.Name, configuredRegion)
	}
	// Keep the original reference only when its parsed UUID still identifies
	// the VPC returned by the backend.
	if !compare.SameUUID(reference.Get(m.VPC, reference.ID), lb.Vpc.Id) {
		m.VPC = reference.Value(reference.ID, types.StringValue(lb.Vpc.Id.String()))
	}
	m.PrivateIP = optionalReferenceState(m.PrivateIP, privateIPID)
	var qosPolicyID *core.UUID
	if qosPolicy != nil {
		qosPolicyID = &qosPolicy.Id
	}
	m.QoSPolicy = optionalReferenceState(m.QoSPolicy, qosPolicyID)
}

// loadBalancerTypeState returns the state of the type. A backend type that
// differs from the prior value only by outer whitespace keeps the prior
// spelling. Some backend deployments omit the type that the API contract
// requires, so the response decodes to "". Then the known prior value stays.
// Without one, the type is the create default appliance, because the backend
// applies it when the type is omitted.
func loadBalancerTypeState(prior types.String, backend loadbalancersdk.LoadBalancerType) types.String {
	if backend != "" {
		return compare.PreservedString(prior, string(backend))
	}
	if !prior.IsNull() && !prior.IsUnknown() && prior.ValueString() != "" {
		return prior
	}
	return types.StringValue(string(loadbalancersdk.LoadBalancerTypeAppliance))
}

// optionalReferenceState returns the state of an optional ID reference whose
// target the backend can assign on its own. An omitted reference stays null,
// so the backend's choice appears only in the flat computed ID. A configured
// reference keeps its representation while it names the backend's object.
// Otherwise it takes the backend's ID, or null when the backend has none, so
// the plan shows the drift.
func optionalReferenceState(prior types.Object, backend *core.UUID) types.Object {
	if prior.IsNull() || backend == nil {
		return types.ObjectNull(reference.AttributeTypes(reference.ID))
	}
	if compare.SameUUID(reference.Get(prior, reference.ID), *backend) {
		return prior
	}
	return reference.Value(reference.ID, types.StringValue(backend.String()))
}

// populateLoadBalancerResourceState maps the API response into resource state.
func populateLoadBalancerResourceState(lb *loadbalancersdk.LoadBalancerDetailSchema, state *LoadBalancerResourceModel) {
	if lb == nil {
		return
	}
	populateLoadBalancerModel(lb, &state.LoadBalancerModel)
	state.PublicIPMode = preservedPublicIPMode(state.PublicIPMode, loadBalancerPublicIPMode(lb))
}

// loadBalancerPublicIPMode derives public_ip_mode from the public addresses,
// because the Load Balancer response carries no mode field. No public IPv4
// address means that public IP is off, and an IPv6 address next to it means
// dual-stack.
func loadBalancerPublicIPMode(lb *loadbalancersdk.LoadBalancerDetailSchema) types.String {
	switch {
	case core.NullableToPtr(lb.PublicAddress) == nil:
		return types.StringNull()
	case core.NullableToPtr(lb.V6PublicAddress) == nil:
		return types.StringValue(string(loadbalancersdk.LoadBalancerPublicIPModeIpv4Only))
	default:
		return types.StringValue(string(loadbalancersdk.LoadBalancerPublicIPModeDualStack))
	}
}

// LoadBalancerDataSourceModel keeps every referenced object flat, as the data
// source conventions require. The resource model nests region, VPC, Private IP,
// and QoS policy as reference objects, so the data source cannot embed it.
type LoadBalancerDataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Description        types.String `tfsdk:"description"`
	Package            types.String `tfsdk:"package"`
	Topology           types.String `tfsdk:"topology"`
	Type               types.String `tfsdk:"type"`
	Region             types.String `tfsdk:"region"`
	RegionID           types.String `tfsdk:"region_id"`
	VPCID              types.String `tfsdk:"vpc_id"`
	VPCName            types.String `tfsdk:"vpc_name"`
	PrivateIPID        types.String `tfsdk:"private_ip_id"`
	QoSPolicyID        types.String `tfsdk:"qos_policy_id"`
	QoSPolicyName      types.String `tfsdk:"qos_policy_name"`
	VIPAddress         types.String `tfsdk:"vip_address"`
	PublicAddress      types.String `tfsdk:"public_address"`
	V6PublicAddress    types.String `tfsdk:"v6_public_address"`
	Status             types.String `tfsdk:"status"`
	ProvisioningStatus types.String `tfsdk:"provisioning_status"`
	AccessLogEnabled   types.Bool   `tfsdk:"access_log_enabled"`
	AccessLogStoreID   types.String `tfsdk:"access_log_store_id"`
	AccessLogStoreName types.String `tfsdk:"access_log_store_name"`
	CreatedAt          types.String `tfsdk:"created_at"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
}

// populateLoadBalancerDataSourceState maps the API response to data source
// state. Unlike the resource, the data source maps a type the backend omits to
// null, because it has no prior value or create default to fall back on.
// preserveConfiguredLoadBalancerValues restores the configured spellings.
func populateLoadBalancerDataSourceState(lb *loadbalancersdk.LoadBalancerDetailSchema, state *LoadBalancerDataSourceModel) {
	if lb == nil {
		return
	}

	state.ID = types.StringValue(lb.Id.String())
	state.Name = types.StringValue(lb.Name)
	state.Description = types.StringPointerValue(core.NullableToPtr(lb.Description))
	state.Package = types.StringValue(string(lb.Package))
	state.Topology = types.StringValue(string(lb.Topology))
	state.Region = types.StringValue(lb.Region.Name)
	state.RegionID = types.StringValue(lb.Region.Id.String())
	state.VPCID = types.StringValue(lb.Vpc.Id.String())
	state.VPCName = types.StringValue(lb.Vpc.Name)
	state.PrivateIPID = types.StringNull()
	if privateIPID := core.NullableToPtr(lb.PrivateIpId); privateIPID != nil {
		state.PrivateIPID = types.StringValue(privateIPID.String())
	}
	state.VIPAddress = types.StringPointerValue(core.NullableToPtr(lb.VipAddress))
	state.PublicAddress = types.StringPointerValue(core.NullableToPtr(lb.PublicAddress))
	state.V6PublicAddress = types.StringPointerValue(core.NullableToPtr(lb.V6PublicAddress))
	state.Status = types.StringValue(string(lb.Status))
	state.ProvisioningStatus = types.StringValue(string(lb.ProvisioningStatus))
	state.CreatedAt = types.StringValue(lb.CreatedAt.Format(time.RFC3339))
	state.UpdatedAt = types.StringValue(lb.UpdatedAt.Format(time.RFC3339))
	state.QoSPolicyID = types.StringNull()
	state.QoSPolicyName = types.StringNull()
	if qosPolicy := core.NullableToPtr(lb.QosPolicy); qosPolicy != nil {
		state.QoSPolicyID = types.StringValue(qosPolicy.Id.String())
		state.QoSPolicyName = types.StringValue(qosPolicy.Name)
	}
	// The backend can omit type, which the SDK decodes as "".
	state.Type = types.StringNull()
	if lb.Type != "" {
		state.Type = types.StringValue(string(lb.Type))
	}
	state.AccessLogEnabled = types.BoolValue(lb.AccessLogEnabled)
	state.AccessLogStoreID = types.StringNull()
	state.AccessLogStoreName = types.StringNull()
	if accessLogStore := core.NullableToPtr(lb.AccessLogStore); accessLogStore != nil {
		state.AccessLogStoreID = types.StringValue(accessLogStore.Id.String())
		state.AccessLogStoreName = types.StringValue(accessLogStore.Name)
	}
}

// preserveConfiguredLoadBalancerValues keeps the configured id, name, region,
// enum, and address filters when they match the backend values, so that UUID
// casing, whitespace, and IP notation do not drift.
func preserveConfiguredLoadBalancerValues(
	lb *loadbalancersdk.LoadBalancerDetailSchema,
	config LoadBalancerDataSourceModel,
	state *LoadBalancerDataSourceModel,
) {
	if lb == nil {
		return
	}
	if compare.SameUUID(config.ID, lb.Id) {
		state.ID = config.ID
	}
	if !config.Name.IsNull() && !config.Name.IsUnknown() && strings.TrimSpace(config.Name.ValueString()) == lb.Name {
		state.Name = config.Name
	}
	if !config.Region.IsNull() && !config.Region.IsUnknown() && strings.TrimSpace(config.Region.ValueString()) == lb.Region.Name {
		state.Region = config.Region
	}
	state.Package = compare.PreservedString(config.Package, string(lb.Package))
	state.Topology = compare.PreservedString(config.Topology, string(lb.Topology))
	if lb.Type != "" {
		state.Type = compare.PreservedString(config.Type, string(lb.Type))
	}
	for _, address := range []struct {
		configured types.String
		backend    *string
		state      *types.String
	}{
		{config.VIPAddress, core.NullableToPtr(lb.VipAddress), &state.VIPAddress},
		{config.PublicAddress, core.NullableToPtr(lb.PublicAddress), &state.PublicAddress},
		{config.V6PublicAddress, core.NullableToPtr(lb.V6PublicAddress), &state.V6PublicAddress},
	} {
		if address.backend != nil && compare.SameIPAddress(address.configured, *address.backend) {
			*address.state = address.configured
		}
	}
}
