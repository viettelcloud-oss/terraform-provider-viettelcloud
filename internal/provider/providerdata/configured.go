package providerdata

import (
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
)

type Configured struct {
	BlockStorage *blockstoragesdk.Client
	DBaaS        *dbaassdk.Client
	FileStorage  *filestoragesdk.Client
	DBaaSCluster *dbaasclustersdk.Client
	LoadBalancer *loadbalancersdk.Client
	Network      *networksdk.Client
	Project      *projectsdk.Client
	Server       *serversdk.Client
	ProjectID    core.UUID
	// Claims serializes the operations of this provider process that claim
	// the same backend object. Key it by the ID of the claimed root object.
	Claims *keylock.Locker
}
