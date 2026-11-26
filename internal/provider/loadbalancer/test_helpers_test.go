package loadbalancer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/viettelcloud-oss/sdks/go/core"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/reference"
)

var (
	loadBalancerTestProjectID   = core.UUID{1}
	loadBalancerTestID          = core.UUID{2}
	loadBalancerTestRegionID    = core.UUID{3}
	loadBalancerTestVPCID       = core.UUID{4}
	loadBalancerTestPrivateIPID = core.UUID{5}
	loadBalancerTestQoSID       = core.UUID{6}
)

var (
	serverGroupTestProjectID = core.UUID{1}
	serverGroupTestLBID      = core.UUID{2}
	serverGroupTestID        = core.UUID{3}
)

type loadBalancerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f loadBalancerRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func loadBalancerJSON(t *testing.T, lb *loadbalancersdk.LoadBalancerDetailSchema) string {
	t.Helper()
	body, err := json.Marshal(lb)
	if err != nil {
		t.Fatalf("marshal Load Balancer: %v", err)
	}
	return string(body)
}

func serverGroupHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

var (
	l7RuleTestProjectID = core.UUID{9}
	l7RuleTestPolicyID  = core.UUID{1}
	l7RuleTestRuleID    = core.UUID{2}
)

type l7RuleRoundTripFunc func(*http.Request) (*http.Response, error)

func (f l7RuleRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newL7RuleTestClient(t *testing.T, fn l7RuleRoundTripFunc) *loadbalancersdk.Client {
	t.Helper()
	httpClient := &http.Client{Transport: fn}
	client, err := loadbalancersdk.NewClient(
		"https://lb.example.com",
		loadbalancersdk.WithHTTPClient(httpClient),
		loadbalancersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return client
}

func newL7RuleTestDataSource(t *testing.T, fn l7RuleRoundTripFunc) *L7RuleDataSource {
	t.Helper()
	d := NewL7RuleDataSource().(*L7RuleDataSource)
	d.client = newL7RuleTestClient(t, fn)
	d.projectID = l7RuleTestProjectID
	return d
}

func l7RuleTestHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sampleServerGroupDetail(
	id core.UUID,
	name string,
	protocol loadbalancersdk.ServerGroupProtocolType,
	algorithm loadbalancersdk.ServerGroupAlgorithm,
) *loadbalancersdk.ServerGroupDetailSchema {
	desc := "test description"
	h2 := false
	spType := loadbalancersdk.ServerGroupSessionPersistenceNONE
	sg := &loadbalancersdk.ServerGroupDetailSchema{
		Id:                     id,
		Name:                   name,
		Protocol:               protocol,
		Algorithm:              algorithm,
		Description:            core.NullableFromPtr(&desc),
		EnableH2:               core.NullableFromPtr(&h2),
		EnableHealthCheck:      false,
		SessionPersistenceType: core.NullableFromPtr(&spType),
		ProvisioningStatus:     loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:                 loadbalancersdk.LoadBalancingStatusONLINE,
		MemberCount:            0,
		CreatedAt:              time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:              time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	// The backend reports these fields as null. An unset Nullable without
	// omitempty would reach the mock response as a zero value instead.
	sg.BackendTlsMode.SetNull()
	sg.SessionPersistenceCookieName.SetNull()
	sg.MonitorType.SetNull()
	sg.MonitorDelay.SetNull()
	sg.MonitorTimeout.SetNull()
	sg.MonitorMaxRetries.SetNull()
	sg.MonitorMaxRetriesDown.SetNull()
	sg.MonitorPath.SetNull()
	sg.MonitorHttpMethod.SetNull()
	sg.MonitorExpectedCodes.SetNull()
	return sg
}

func sampleServerGroupDetailWithHealthCheck(
	id core.UUID,
	name string,
	protocol loadbalancersdk.ServerGroupProtocolType,
	algorithm loadbalancersdk.ServerGroupAlgorithm,
) *loadbalancersdk.ServerGroupDetailSchema {
	sg := sampleServerGroupDetail(id, name, protocol, algorithm)
	sg.EnableHealthCheck = true
	monitorType := loadbalancersdk.ServerGroupMonitorTypeHTTP
	sg.MonitorType.Set(monitorType)
	delay := 10
	sg.MonitorDelay.Set(delay)
	timeout := 5
	sg.MonitorTimeout.Set(timeout)
	retries := 3
	sg.MonitorMaxRetries.Set(retries)
	retriesDown := 3
	sg.MonitorMaxRetriesDown.Set(retriesDown)
	path := "/health"
	sg.MonitorPath.Set(path)
	method := loadbalancersdk.ServerGroupMonitorHTTPMethodGET
	sg.MonitorHttpMethod.Set(method)
	codes := "200"
	sg.MonitorExpectedCodes.Set(codes)
	return sg
}

func sampleServerGroupDetailJSON(t *testing.T, sg *loadbalancersdk.ServerGroupDetailSchema) string {
	t.Helper()
	b, err := json.Marshal(sg)
	if err != nil {
		t.Fatalf("marshal server group: %v", err)
	}
	return string(b)
}

func sampleL7RuleSchema() *loadbalancersdk.L7RuleSchema {
	return &loadbalancersdk.L7RuleSchema{
		Id:                 l7RuleTestRuleID,
		L7policyId:         l7RuleTestPolicyID,
		Type:               loadbalancersdk.L7RuleTypePATH,
		CompareType:        loadbalancersdk.L7RuleCompareTypeSTARTSWITH,
		Value:              "/api",
		Invert:             false,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		CreatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func l7RuleJSON(rule *loadbalancersdk.L7RuleSchema) string {
	b, err := json.Marshal(rule)
	if err != nil {
		panic(fmt.Sprintf("marshal l7 rule: %v", err))
	}
	return string(b)
}

var (
	listenerTestProjectID      = core.UUID{1}
	listenerTestLoadBalancerID = core.UUID{2}
	listenerTestID             = core.UUID{3}
	listenerTestServerGroupID  = core.UUID{4}
	listenerTestTLSCertID      = core.UUID{5}
	listenerTestSNICertID      = core.UUID{6}
)

var listenerTestLoadBalancerPath = fmt.Sprintf("/v2/load-balancing/loadbalancers/%s/", listenerTestLoadBalancerID)

type listenerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f listenerRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// newListenerTestResource answers every GET of Load Balancer
// listenerTestLoadBalancerID with ACTIVE, so that the idle wait before a
// Listener mutation passes at once. fn answers every other request.
func newListenerTestResource(t *testing.T, fn listenerRoundTripFunc) *ListenerResource {
	t.Helper()
	return newListenerTestResourceWithParent(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && req.URL.Path == listenerTestLoadBalancerPath {
			return listenerJSONResponse(http.StatusOK, loadBalancerResponseJSON(loadbalancersdk.LoadBalancingProvisioningStatusACTIVE)), nil
		}
		return fn(req)
	})
}

// newListenerTestResourceWithParent sends every request to fn, including the
// GET of the parent Load Balancer.
func newListenerTestResourceWithParent(t *testing.T, fn listenerRoundTripFunc) *ListenerResource {
	t.Helper()
	httpClient := &http.Client{Transport: fn}
	client, err := loadbalancersdk.NewClient(
		"https://lb.example.com",
		loadbalancersdk.WithHTTPClient(httpClient),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	r := NewListenerResource().(*ListenerResource)
	r.client = client
	r.projectID = listenerTestProjectID
	r.claims = keylock.New()
	return r
}

func listenerSchema(t *testing.T) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&ListenerResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	return response.Schema
}

func defaultTestListenerModel() ListenerResourceModel {
	return ListenerResourceModel{
		ListenerModel:  ListenerModel{AllowedCIDRs: types.SetNull(types.StringType)},
		LoadBalancer:   types.ObjectNull(reference.AttributeTypes(reference.ID)),
		ServerGroup:    types.ObjectNull(reference.AttributeTypes(reference.ID)),
		DefaultTLSCert: types.ObjectNull(reference.AttributeTypes(reference.ID)),
		SNITLSCerts:    types.SetNull(reference.ObjectType(reference.ID)),
	}
}

func listenerTestReference(id string) types.Object {
	return reference.Value(reference.ID, types.StringValue(id))
}

func listenerTestReferenceSet(ids ...string) types.Set {
	return reference.Set(reference.ID, ids)
}

func sampleListenerDetailJSON(id, lbID core.UUID, name, protocol string, port int, provStatus, opStatus string) string {
	return fmt.Sprintf(`{
		"id": %q,
		"name": %q,
		"protocol": %q,
		"protocol_port": %d,
		"listener_timeout": 50000,
		"server_timeout": 50000,
		"server_connecting_timeout": 5000,
		"provisioning_status": %q,
		"status": %q,
		"load_balancer": {
			"id": %q,
			"name": "test-lb"
		},
		"server_group": null,
		"default_tls_cert_id": null,
		"sni_tls_certs": [],
		"allowed_cidrs": [],
		"enable_h2": null,
		"set_x_forwarded_for": null,
		"set_x_forwarded_port": null,
		"set_x_forwarded_proto": null,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z"
	}`, id, name, protocol, port, provStatus, opStatus, lbID)
}

func listenerJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

var (
	memberTestProjectID      = core.UUID{1}
	memberTestServerGroupID  = core.UUID{2}
	memberTestID             = core.UUID{3}
	memberTestLoadBalancerID = core.UUID{4}
)

// memberTestParents answers the requests about the parents of the test
// Member: the owner search of Server Group memberTestServerGroupID, and the
// GETs of that Server Group and of Load Balancer memberTestLoadBalancerID.
// Each GET reports the next status of its list and then repeats the last
// one. An empty list reports ACTIVE.
type memberTestParents struct {
	serverGroup      []loadbalancersdk.LoadBalancingProvisioningStatus
	loadBalancer     []loadbalancersdk.LoadBalancingProvisioningStatus
	serverGroupGets  int
	loadBalancerGets int
	// orphan makes the owner search find no Load Balancer that owns the
	// Server Group.
	orphan bool
	// serverGroupMissing makes the GET of the Server Group answer 404 while
	// the owner search still finds it.
	serverGroupMissing bool
}

// roundTrip answers req when it is a parent request, and reports whether it
// did.
func (p *memberTestParents) roundTrip(t *testing.T, req *http.Request) (*http.Response, bool) {
	t.Helper()
	if req.Method != http.MethodGet {
		return nil, false
	}
	loadBalancers := "/v2/load-balancing/loadbalancers/"
	loadBalancer := loadBalancers + memberTestLoadBalancerID.String() + "/"
	serverGroups := loadBalancer + "server-groups/"
	serverGroup := serverGroups + memberTestServerGroupID.String() + "/"
	switch req.URL.Path {
	case loadBalancers:
		return serverGroupMemberHTTPResponse(req, http.StatusOK, memberTestJSON(t, map[string]any{
			"count":   1,
			"results": []map[string]any{{"id": memberTestLoadBalancerID, "name": "lb", "provisioning_status": "ACTIVE"}},
		})), true
	case serverGroups:
		groups := []map[string]any{{"id": memberTestServerGroupID, "name": "group", "provisioning_status": "ACTIVE"}}
		if p.orphan {
			groups = []map[string]any{}
		}
		return serverGroupMemberHTTPResponse(req, http.StatusOK, memberTestJSON(t, map[string]any{
			"count":   len(groups),
			"results": groups,
		})), true
	case serverGroup:
		p.serverGroupGets++
		if p.serverGroupMissing {
			return serverGroupMemberHTTPResponse(req, http.StatusNotFound, `{"detail":"not found"}`), true
		}
		return serverGroupMemberHTTPResponse(req, http.StatusOK, memberTestJSON(t, map[string]any{
			"id":                  memberTestServerGroupID,
			"name":                "group",
			"provisioning_status": memberTestStatus(p.serverGroup, p.serverGroupGets),
		})), true
	case loadBalancer:
		p.loadBalancerGets++
		return serverGroupMemberHTTPResponse(req, http.StatusOK, memberTestJSON(t, map[string]any{
			"id":                  memberTestLoadBalancerID,
			"name":                "lb",
			"provisioning_status": memberTestStatus(p.loadBalancer, p.loadBalancerGets),
		})), true
	}
	return nil, false
}

// memberTestStatus returns the status that the nth GET reports, counting
// from 1.
func memberTestStatus(statuses []loadbalancersdk.LoadBalancingProvisioningStatus, n int) loadbalancersdk.LoadBalancingProvisioningStatus {
	if len(statuses) == 0 {
		return loadbalancersdk.LoadBalancingProvisioningStatusACTIVE
	}
	return statuses[min(n, len(statuses))-1]
}

func memberTestJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test response: %v", err)
	}
	return string(b)
}

// newServerGroupMemberTestResource returns a Member resource whose parents
// are ACTIVE. Requests about the Member itself go to transport.
func newServerGroupMemberTestResource(t *testing.T, transport http.RoundTripper) *ServerGroupMemberResource {
	t.Helper()
	return newServerGroupMemberTestResourceWithParents(t, &memberTestParents{}, transport)
}

func newServerGroupMemberTestResourceWithParents(t *testing.T, parents *memberTestParents, transport http.RoundTripper) *ServerGroupMemberResource {
	t.Helper()
	httpClient := &http.Client{Transport: loadBalancerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if resp, ok := parents.roundTrip(t, req); ok {
			return resp, nil
		}
		return transport.RoundTrip(req)
	})}
	client, err := loadbalancersdk.NewClient(
		"https://load-balancer.test",
		loadbalancersdk.WithHTTPClient(httpClient),
		loadbalancersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create load balancer client: %v", err)
	}
	return &ServerGroupMemberResource{
		client:    client,
		projectID: memberTestProjectID,
		claims:    keylock.New(),
	}
}

func newServerGroupMemberTestDataSource(t *testing.T, transport http.RoundTripper) *ServerGroupMemberDataSource {
	t.Helper()
	httpClient := &http.Client{Transport: transport}
	client, err := loadbalancersdk.NewClient(
		"https://load-balancer.test",
		loadbalancersdk.WithHTTPClient(httpClient),
		loadbalancersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create load balancer client: %v", err)
	}
	return &ServerGroupMemberDataSource{
		client:    client,
		projectID: memberTestProjectID,
	}
}

func serverGroupMemberHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sampleServerGroupMemberDetail(
	id core.UUID,
	name string,
	address string,
	protocolPort int,
	weight int,
	backup bool,
) *loadbalancersdk.ServerGroupMemberSchema {
	return &loadbalancersdk.ServerGroupMemberSchema{
		Id:                 id,
		Name:               name,
		Address:            address,
		ProtocolPort:       protocolPort,
		Weight:             weight,
		Backup:             backup,
		ProvisioningStatus: loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:             loadbalancersdk.LoadBalancingStatusONLINE,
		CreatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func sampleServerGroupMemberDetailJSON(t *testing.T, member *loadbalancersdk.ServerGroupMemberSchema) string {
	t.Helper()
	b, err := json.Marshal(member)
	if err != nil {
		t.Fatalf("marshal server group member: %v", err)
	}
	return string(b)
}

var (
	l7PolicyTestID            = core.UUID{1}
	l7PolicyTestListenerID    = core.UUID{2}
	l7PolicyTestProjectID     = core.UUID{9}
	l7PolicyTestServerGroupID = core.UUID{3}
)

type l7PolicyRoundTripFunc func(*http.Request) (*http.Response, error)

func (f l7PolicyRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func l7PolicyTestClient(t *testing.T, roundTrip l7PolicyRoundTripFunc) *loadbalancersdk.Client {
	t.Helper()
	client, err := loadbalancersdk.NewClient(
		"https://loadbalancer.test",
		loadbalancersdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		loadbalancersdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create load balancer client: %v", err)
	}
	return client
}

func l7PolicyTestHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sampleL7PolicyDetail() *loadbalancersdk.L7PolicyDetailSchema {
	name := "example-policy"
	desc := "example description"
	code := 301
	prefix := "https://example.com"
	return &loadbalancersdk.L7PolicyDetailSchema{
		Id:                  l7PolicyTestID,
		ListenerId:          l7PolicyTestListenerID,
		Name:                core.NullableFromPtr(&name),
		Description:         core.NullableFromPtr(&desc),
		Action:              loadbalancersdk.L7PolicyActionREDIRECTPREFIX,
		Position:            1,
		RedirectHttpCode:    core.NullableFromPtr(&code),
		RedirectPrefix:      core.NullableFromPtr(&prefix),
		RedirectServerGroup: l7PolicyNullNullable[loadbalancersdk.NestedServerGroupSchema](),
		RedirectUrl:         l7PolicyNullNullable[string](),
		RuleCount:           0,
		ProvisioningStatus:  loadbalancersdk.LoadBalancingProvisioningStatusACTIVE,
		Status:              loadbalancersdk.LoadBalancingStatusONLINE,
		CreatedAt:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// l7PolicyNullNullable returns a Nullable holding an explicit null, the value
// that the backend sends for a nullable field that has none. An unset
// Nullable marshals as the zero value instead.
func l7PolicyNullNullable[T any]() core.Nullable[T] {
	var n core.Nullable[T]
	n.SetNull()
	return n
}

// l7PolicyNullableEquals reports whether n holds want; an unset or null n
// holds nothing.
func l7PolicyNullableEquals[T comparable](n core.Nullable[T], want T) bool {
	value, err := n.Get()
	return err == nil && value == want
}

func l7PolicyDetailJSON(p *loadbalancersdk.L7PolicyDetailSchema) string {
	b, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("marshal detail: %v", err))
	}
	return string(b)
}

func sampleL7PolicySchema() *loadbalancersdk.L7PolicySchema {
	d := sampleL7PolicyDetail()
	return l7PolicyDetailToSchema(d)
}

func l7PolicySchemaJSON(p *loadbalancersdk.L7PolicySchema) string {
	b, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("marshal schema: %v", err))
	}
	return string(b)
}

// l7PolicyTestDataSourceAttributeTypes reports the listener and the redirect
// server group as flat IDs, as the data source does.
func l7PolicyTestDataSourceAttributeTypes() map[string]tftypes.Type {
	return map[string]tftypes.Type{
		"id":                       tftypes.String,
		"listener_id":              tftypes.String,
		"name":                     tftypes.String,
		"description":              tftypes.String,
		"action":                   tftypes.String,
		"position":                 tftypes.Number,
		"redirect_server_group_id": tftypes.String,
		"redirect_url":             tftypes.String,
		"redirect_prefix":          tftypes.String,
		"redirect_http_code":       tftypes.Number,
		"rule_count":               tftypes.Number,
		"provisioning_status":      tftypes.String,
		"status":                   tftypes.String,
		"created_at":               tftypes.String,
		"updated_at":               tftypes.String,
	}
}

var l7PolicyTestReferenceType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"id": tftypes.String}}

func l7PolicyTestResourceAttributeTypes() map[string]tftypes.Type {
	attributeTypes := l7PolicyTestDataSourceAttributeTypes()
	delete(attributeTypes, "listener_id")
	delete(attributeTypes, "redirect_server_group_id")
	attributeTypes["listener"] = l7PolicyTestReferenceType
	attributeTypes["redirect_server_group"] = l7PolicyTestReferenceType
	return attributeTypes
}

// l7PolicyTestReference returns a reference object for id, or a null object
// for a nil id.
func l7PolicyTestReference(id any) tftypes.Value {
	if id == nil {
		return tftypes.NewValue(l7PolicyTestReferenceType, nil)
	}
	return tftypes.NewValue(l7PolicyTestReferenceType, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, id)})
}
