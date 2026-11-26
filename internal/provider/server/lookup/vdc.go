package lookup

import (
	"context"
	"fmt"
	"iter"

	"github.com/viettelcloud-oss/terraform-provider-viettelcloud/internal/finder"

	"github.com/viettelcloud-oss/sdks/go/core"
	serversdk "github.com/viettelcloud-oss/sdks/go/server"
)

// vdcOrdering keeps the page order stable while the finder reads several
// pages. The default order is -updated_at, so an operator edit during the read
// would move a vDC between pages. created_at never changes, and the backend
// always adds pk as the last sort key.
const vdcOrdering = "created_at"

type VDCLister interface {
	ListVdcsIter(context.Context, serversdk.ListVdcsParams) iter.Seq2[*serversdk.VDCSchema, error]
}

// VDCFilter selects vDCs. ID and Zone are matched client-side because the list
// API has no id filter and filters zones by ID only. ZoneID is the ID that the
// caller resolved from Zone, and it only narrows the request.
type VDCFilter struct {
	ID     *core.UUID
	Name   *string
	Region *string
	Zone   *string
	ZoneID *core.UUID
}

func (f VDCFilter) String() string {
	return finder.Criteria(
		finder.Part("id", f.ID), finder.Text("name", f.Name),
		finder.Text("region", f.Region), finder.Text("zone", f.Zone),
	)
}

// matches compares names exactly, because the list filters name and region
// with a case-insensitive substring match.
func (f VDCFilter) matches(vdc *serversdk.VDCSchema) bool {
	if f.ID != nil && vdc.Id != *f.ID {
		return false
	}
	if name := finder.Trim(f.Name); name != "" && vdc.Name != name {
		return false
	}
	if region := finder.Trim(f.Region); region != "" && vdc.Region.Name != region {
		return false
	}
	zone := finder.Trim(f.Zone)
	return zone == "" || vdc.Zone.Name == zone
}

func (f VDCFilter) params(projectID core.UUID) serversdk.ListVdcsParams {
	params := serversdk.ListVdcsParams{ProjectID: projectID, ZoneId: f.ZoneID, Ordering: new(vdcOrdering)}
	if name := finder.Trim(f.Name); name != "" {
		params.Name = &name
	}
	if region := finder.Trim(f.Region); region != "" {
		params.Region = &region
	}
	return params
}

type VDCFinder struct {
	client    VDCLister
	projectID core.UUID
}

var _ finder.Interface[VDCFilter, serversdk.VDCSchema] = (*VDCFinder)(nil)

func NewVDCFinder(client VDCLister, projectID core.UUID) *VDCFinder {
	return &VDCFinder{client: client, projectID: projectID}
}

func (f *VDCFinder) Find(ctx context.Context, filter VDCFilter) ([]serversdk.VDCSchema, error) {
	return finder.Collect(f.client.ListVdcsIter(ctx, filter.params(f.projectID)), "vDC", filter.String(), filter.matches)
}

func (f *VDCFinder) Resolve(ctx context.Context, filter VDCFilter) (serversdk.VDCSchema, error) {
	candidates, err := f.Find(ctx, filter)
	if err != nil {
		return serversdk.VDCSchema{}, err
	}
	return finder.ExactlyOne(candidates, "vDC", filter.String(), vdcSummary)
}

func vdcSummary(vdc serversdk.VDCSchema) string {
	return fmt.Sprintf("id=%s (name=%q, region=%q, zone=%q)", vdc.Id, vdc.Name, vdc.Region.Name, vdc.Zone.Name)
}
