package lookup

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	blockstoragesdk "github.com/viettelcloud-oss/sdks/go/blockstorage"
	"github.com/viettelcloud-oss/sdks/go/core"
)

type VolumeTypeLister interface {
	ListVolumeTypesIter(context.Context, blockstoragesdk.ListVolumeTypesParams) iter.Seq2[*blockstoragesdk.VolumeTypeSchema, error]
}

type VolumeTypeFilter struct {
	Name              *string
	DiskType          *blockstoragesdk.VolumeTypeDiskType
	Speed             *blockstoragesdk.VolumeTypeSpeed
	Multiattach       *bool
	EncryptionKeySize *blockstoragesdk.VolumeTypeEncryptKeySize
	BackendName       *string
	ZoneID            *core.UUID
}

func (f VolumeTypeFilter) String() string {
	return finder.Criteria(
		finder.Text("name", f.Name),
		finder.Text("disk_type", (*string)(f.DiskType)), finder.Text("speed", (*string)(f.Speed)), finder.Part("multiattach", f.Multiattach), finder.Text("encryption_key_size", (*string)(f.EncryptionKeySize)),
		finder.Text("volume_backend_name", f.BackendName),
		finder.Part("zone_id", f.ZoneID),
	)
}

func (f VolumeTypeFilter) matches(volumeType *blockstoragesdk.VolumeTypeSchema) bool {
	if name := finder.Trim(f.Name); name != "" && volumeType.Name != name {
		return false
	}
	if backendName := finder.Trim(f.BackendName); backendName != "" && volumeType.VolumeBackendName != backendName {
		return false
	}
	if value := finder.Trim((*string)(f.DiskType)); value != "" && string(volumeType.DiskType.Value) != value {
		return false
	}
	if value := finder.Trim((*string)(f.Speed)); value != "" && string(volumeType.Speed.Value) != value {
		return false
	}
	if f.Multiattach != nil && volumeType.Multiattach != *f.Multiattach {
		return false
	}
	if value := finder.Trim((*string)(f.EncryptionKeySize)); value != "" && string(volumeType.EncryptionKeySize.Value) != value {
		return false
	}
	return f.ZoneID == nil || volumeType.Zone.Id == *f.ZoneID
}

func (f VolumeTypeFilter) params() blockstoragesdk.ListVolumeTypesParams {
	params := blockstoragesdk.ListVolumeTypesParams{ZoneId: f.ZoneID, Multiattach: f.Multiattach}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if value := finder.Trim((*string)(f.DiskType)); value != "" {
		params.DiskType = new(blockstoragesdk.VolumeTypeDiskType(value))
	}
	if value := finder.Trim((*string)(f.Speed)); value != "" {
		params.Speed = new(blockstoragesdk.VolumeTypeSpeed(value))
	}
	if value := finder.Trim((*string)(f.EncryptionKeySize)); value != "" {
		params.EncryptionKeySize = new(blockstoragesdk.VolumeTypeEncryptKeySize(value))
	}
	return params
}

type VolumeTypeFinder struct {
	client VolumeTypeLister
}

type VolumeTypeResolveRequest struct {
	Filter        VolumeTypeFilter
	ZoneID        *core.UUID
	RequestedSize int
}

type VolumeTypeResolveFunc func(context.Context, VolumeTypeResolveRequest) (blockstoragesdk.VolumeTypeSchema, error)

var _ finder.Interface[VolumeTypeFilter, blockstoragesdk.VolumeTypeSchema] = (*VolumeTypeFinder)(nil)

func NewVolumeTypeFinder(client VolumeTypeLister) *VolumeTypeFinder {
	return &VolumeTypeFinder{client: client}
}

func (f *VolumeTypeFinder) Find(ctx context.Context, filter VolumeTypeFilter) ([]blockstoragesdk.VolumeTypeSchema, error) {
	return finder.Collect(f.client.ListVolumeTypesIter(ctx, filter.params()), "volume type", filter.String(), filter.matches)
}

func (f *VolumeTypeFinder) Resolve(ctx context.Context, filter VolumeTypeFilter) (blockstoragesdk.VolumeTypeSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return blockstoragesdk.VolumeTypeSchema{}, err
	}
	return finder.ExactlyOne(candidates, "volume type", filter.String(), volumeTypeSummary)
}

func (f *VolumeTypeFinder) ResolveForCreate(
	ctx context.Context,
	request VolumeTypeResolveRequest,
) (blockstoragesdk.VolumeTypeSchema, error) {
	filter := request.Filter
	filter.ZoneID = request.ZoneID
	if finder.Trim(filter.Name) == "" && finder.Trim((*string)(filter.DiskType)) == "" && finder.Trim((*string)(filter.Speed)) == "" && filter.Multiattach == nil && finder.Trim((*string)(filter.EncryptionKeySize)) == "" {
		return blockstoragesdk.VolumeTypeSchema{}, fmt.Errorf("volume type criteria must not be empty")
	}
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return blockstoragesdk.VolumeTypeSchema{}, err
	}

	ready := make([]blockstoragesdk.VolumeTypeSchema, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Status.Value == blockstoragesdk.VolumeTypeStatusEnabled &&
			candidate.State.Value == blockstoragesdk.VolumeTypeStateUp {
			ready = append(ready, candidate)
		}
	}
	if len(ready) == 0 && len(candidates) > 0 {
		descriptions := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			descriptions = append(descriptions, volumeTypeSummary(candidate))
		}
		return blockstoragesdk.VolumeTypeSchema{}, fmt.Errorf(
			"volume type (%s) has no enabled, up candidate: %s",
			filter.String(),
			strings.Join(descriptions, ", "),
		)
	}
	volumeType, err := finder.ExactlyOne(ready, "ready volume type", filter.String(), volumeTypeSummary)
	if err != nil {
		return blockstoragesdk.VolumeTypeSchema{}, err
	}
	if request.RequestedSize < 1 {
		return blockstoragesdk.VolumeTypeSchema{}, fmt.Errorf("volume size must be greater than zero, got %d", request.RequestedSize)
	}
	if volumeType.MaxVolumeSize > 0 && request.RequestedSize > volumeType.MaxVolumeSize {
		return blockstoragesdk.VolumeTypeSchema{}, fmt.Errorf(
			"volume type %q supports a maximum size of %d GB, requested %d GB",
			volumeType.Name,
			volumeType.MaxVolumeSize,
			request.RequestedSize,
		)
	}
	return volumeType, nil
}

func volumeTypeSummary(volumeType blockstoragesdk.VolumeTypeSchema) string {
	return fmt.Sprintf(
		"id=%s (name=%q, zone=%q, disk_type=%q, speed=%q, multiattach=%t, encryption_key_size=%q, status=%s, state=%s)",
		volumeType.Id,
		volumeType.Name,
		volumeType.Zone.Name,
		volumeType.DiskType.Value, volumeType.Speed.Value, volumeType.Multiattach, volumeType.EncryptionKeySize.Value,
		volumeType.Status.Value,
		volumeType.State.Value,
	)
}
