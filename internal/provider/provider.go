package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
	dbaassdk "github.com/viettelcloud-oss/sdks/go/dbaas"
	dbaasclustersdk "github.com/viettelcloud-oss/sdks/go/dbaascluster"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"
	loadbalancersdk "github.com/viettelcloud-oss/sdks/go/loadbalancer"
	networksdk "github.com/viettelcloud-oss/sdks/go/network"
	projectsdk "github.com/viettelcloud-oss/sdks/go/project"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/keylock"
	blockstorageprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/blockstorage"
	dbaasprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaas"
	dbaasclusterprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/dbaascluster"
	filestorageprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/filestorage"
	loadbalancerprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/loadbalancer"
	networkprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/network"
	projectprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/project/lookup"
	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/providerdata"
	serverprovider "github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/provider/server"
)

var _ provider.Provider = &ViettelCloudProvider{}

type ViettelCloudProvider struct {
	version string
}

type ViettelCloudProviderModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	Token     types.String `tfsdk:"token"`
	ProjectID types.String `tfsdk:"project_id"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ViettelCloudProvider{version: version}
	}
}

func (p *ViettelCloudProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "viettelcloud"
	resp.Version = p.version
}

func (p *ViettelCloudProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Viettel Cloud Provider.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Required:    true,
				Description: "API endpoint URL",
			},
			"token": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Personal access token.",
			},
			"project_id": schema.StringAttribute{
				Required:    true,
				Description: "Project ID (UUID) or project slug. If the value is a UUID, it is used directly; otherwise it is resolved as a project slug.",
			},
		},
	}
}

func (p *ViettelCloudProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config ViettelCloudProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.Endpoint.IsUnknown() || config.Token.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider configuration", "endpoint and token must be known values.")
		return
	}
	if config.Endpoint.IsNull() || config.Token.IsNull() {
		resp.Diagnostics.AddError("Missing provider configuration", "endpoint and token are required.")
		return
	}

	httpClient := &http.Client{Timeout: 60 * time.Second}
	blockStorageClient, err := blockstoragesdk.NewClient(
		config.Endpoint.ValueString(),
		blockstoragesdk.WithPAT(config.Token.ValueString()),
		blockstoragesdk.WithHTTPClient(httpClient),
		blockstoragesdk.WithRetry(core.DefaultRetry()),
		blockstoragesdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create block storage client: %s", err))
		return
	}

	dbaasClient, err := dbaassdk.NewClient(
		config.Endpoint.ValueString(),
		dbaassdk.WithPAT(config.Token.ValueString()),
		dbaassdk.WithHTTPClient(httpClient),
		dbaassdk.WithRetry(core.DefaultRetry()),
		dbaassdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create DBaaS client: %s", err))
		return
	}

	dbaasClusterClient, err := dbaasclustersdk.NewClient(
		config.Endpoint.ValueString(),
		dbaasclustersdk.WithPAT(config.Token.ValueString()),
		dbaasclustersdk.WithHTTPClient(httpClient),
		dbaasclustersdk.WithRetry(core.DefaultRetry()),
		dbaasclustersdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create DBaaS cluster client: %s", err))
		return
	}

	networkClient, err := networksdk.NewClient(
		config.Endpoint.ValueString(),
		networksdk.WithPAT(config.Token.ValueString()),
		networksdk.WithHTTPClient(httpClient),
		networksdk.WithRetry(core.DefaultRetry()),
		networksdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create network client: %s", err))
		return
	}

	projectClient, err := projectsdk.NewClient(
		config.Endpoint.ValueString(),
		projectsdk.WithPAT(config.Token.ValueString()),
		projectsdk.WithHTTPClient(httpClient),
		projectsdk.WithRetry(core.DefaultRetry()),
		projectsdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create project client: %s", err))
		return
	}

	serverClient, err := serversdk.NewClient(
		config.Endpoint.ValueString(),
		serversdk.WithPAT(config.Token.ValueString()),
		serversdk.WithHTTPClient(httpClient),
		serversdk.WithRetry(core.DefaultRetry()),
		serversdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create server client: %s", err))
		return
	}

	projectID, diags := resolveConfiguredProject(ctx, config, lookup.NewProjectFinder(projectClient).Resolve)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	loadBalancerClient, err := loadbalancersdk.NewClient(
		config.Endpoint.ValueString(),
		loadbalancersdk.WithPAT(config.Token.ValueString()),
		loadbalancersdk.WithHTTPClient(httpClient),
		loadbalancersdk.WithRetry(core.DefaultRetry()),
		loadbalancersdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create load balancer client: %s", err))
		return
	}

	fileStorageClient, err := filestoragesdk.NewClient(
		config.Endpoint.ValueString(),
		filestoragesdk.WithPAT(config.Token.ValueString()),
		filestoragesdk.WithHTTPClient(httpClient),
		filestoragesdk.WithRetry(core.DefaultRetry()),
		filestoragesdk.WithUserAgent("terraform-provider-viettelcloud/"+p.version),
	)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create API client", fmt.Sprintf("Unable to create file storage client: %s", err))
		return
	}

	data := &providerdata.Configured{
		BlockStorage: blockStorageClient,
		DBaaS:        dbaasClient,
		FileStorage:  fileStorageClient,
		DBaaSCluster: dbaasClusterClient,
		LoadBalancer: loadBalancerClient,
		Network:      networkClient,
		Project:      projectClient,
		Server:       serverClient,
		ProjectID:    projectID,
		Claims:       keylock.New(),
	}
	resp.DataSourceData = data
	resp.ResourceData = data
}

func resolveConfiguredProject(
	ctx context.Context,
	config ViettelCloudProviderModel,
	resolve lookup.ProjectResolveFunc,
) (core.UUID, diag.Diagnostics) {
	var diags diag.Diagnostics
	if config.ProjectID.IsUnknown() {
		diags.AddError("Unknown project reference", "project_id must be a known value.")
		return core.UUID{}, diags
	}
	if config.ProjectID.IsNull() || strings.TrimSpace(config.ProjectID.ValueString()) == "" {
		diags.AddError("Missing project reference", "project_id must be provided as a UUID or project slug.")
		return core.UUID{}, diags
	}

	configured := strings.TrimSpace(config.ProjectID.ValueString())
	if projectID, err := core.ParseUUID(configured); err == nil {
		return projectID, diags
	}

	slug := configured
	project, err := resolve(ctx, lookup.ProjectFilter{Slug: &slug})
	if err != nil {
		diags.AddError("Failed to resolve project", fmt.Sprintf("Unable to resolve project_id %q as a UUID or project slug: %s", configured, err))
		return core.UUID{}, diags
	}
	return project.Id, diags
}

func (p *ViettelCloudProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		blockstorageprovider.NewVolumeResource,
		blockstorageprovider.NewVolumeBackupResource,
		blockstorageprovider.NewVolumeBackupPolicyResource,
		blockstorageprovider.NewVolumeSnapshotResource,
		blockstorageprovider.NewVolumeSnapshotPolicyResource,
		dbaasprovider.NewInstanceResource,
		dbaasprovider.NewBackupResource,
		filestorageprovider.NewFilesystemSnapshotResource,
		loadbalancerprovider.NewL7PolicyResource,
		loadbalancerprovider.NewServerGroupResource,
		loadbalancerprovider.NewServerGroupMemberResource,
		filestorageprovider.NewFilesystemSnapshotPolicyResource,
		dbaasclusterprovider.NewDBClusterResource,
		networkprovider.NewVPCResource,
		networkprovider.NewSubnetResource,
		networkprovider.NewElasticIPResource,
		networkprovider.NewPrivateIPResource,
		networkprovider.NewSecurityGroupResource,
		networkprovider.NewSecurityGroupRuleResource,
		networkprovider.NewVPCPeeringResource,
		serverprovider.NewServerResource,
		serverprovider.NewPlacementGroupResource,
		serverprovider.NewCustomImageResource,
		serverprovider.NewKeyPairResource,
		filestorageprovider.NewFilesystemResource,
		loadbalancerprovider.NewLoadBalancerResource,
		projectprovider.NewCustomRoleResource,
		projectprovider.NewMemberRolesResource,
		loadbalancerprovider.NewListenerResource,
		loadbalancerprovider.NewL7RuleResource,
	}
}

func (p *ViettelCloudProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		blockstorageprovider.NewVolumeDataSource,
		blockstorageprovider.NewVolumeBackupDataSource,
		blockstorageprovider.NewVolumeBackupPolicyDataSource,
		blockstorageprovider.NewVolumeSnapshotDataSource,
		blockstorageprovider.NewVolumeSnapshotPolicyDataSource,
		dbaasprovider.NewInstanceDataSource,
		dbaasprovider.NewBackupDataSource,
		filestorageprovider.NewFilesystemSnapshotDataSource,
		loadbalancerprovider.NewL7PolicyDataSource,
		loadbalancerprovider.NewServerGroupDataSource,
		loadbalancerprovider.NewServerGroupMemberDataSource,
		filestorageprovider.NewFilesystemSnapshotPolicyDataSource,
		dbaasclusterprovider.NewDBClusterDataSource,
		networkprovider.NewVPCDataSource,
		networkprovider.NewSubnetDataSource,
		networkprovider.NewElasticIPDataSource,
		networkprovider.NewPrivateIPDataSource,
		networkprovider.NewVPCPeeringDataSource,
		serverprovider.NewPlacementGroupDataSource,
		serverprovider.NewCustomImageDataSource,
		serverprovider.NewKeyPairDataSource,
		serverprovider.NewVDCDataSource,
		filestorageprovider.NewFilesystemDataSource,
		networkprovider.NewSecurityGroupDataSource,
		projectprovider.NewCustomRoleDataSource,
		projectprovider.NewMemberRolesDataSource,
		loadbalancerprovider.NewLoadBalancerDataSource,
		loadbalancerprovider.NewListenerDataSource,
		loadbalancerprovider.NewL7RuleDataSource,
	}
}
