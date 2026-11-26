package filestorage

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/viettelcloud-oss/sdks/go/core"
	filestoragesdk "github.com/viettelcloud-oss/sdks/go/filestorage"
)

var (
	testProjectID    = core.UUID{9}
	testFilesystemID = core.UUID{1}
	testSnapshotID   = core.UUID{3}
)

type testRoundTripFunc func(*http.Request) (*http.Response, error)

func (f testRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testClient(t *testing.T, roundTrip testRoundTripFunc) *filestoragesdk.Client {
	t.Helper()
	client, err := filestoragesdk.NewClient(
		"https://filestorage.test",
		filestoragesdk.WithHTTPClient(&http.Client{Transport: roundTrip}),
		filestoragesdk.WithRetry(core.NoRetry()),
	)
	if err != nil {
		t.Fatalf("create file storage client: %v", err)
	}
	return client
}

func testHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sampleFilesystemSnapshot() *filestoragesdk.FilesystemSnapshotSchema {
	return &filestoragesdk.FilesystemSnapshotSchema{
		Id: testSnapshotID, Name: "snapshot", Size: 50, Status: filestoragesdk.FilesystemSnapshotStatusAvailable,
		Filesystem: filestoragesdk.FilesystemSchema{Id: testFilesystemID, Name: "data"},
		OwnerId:    core.UUID{5}, ProjectId: testProjectID,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func filesystemSnapshotJSON(t *testing.T, snapshot *filestoragesdk.FilesystemSnapshotSchema) string {
	t.Helper()
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return string(body)
}

func filesystemSnapshotPageJSON(t *testing.T, snapshots ...filestoragesdk.FilesystemSnapshotSchema) string {
	t.Helper()
	body, err := json.Marshal(filestoragesdk.PagedFilesystemSnapshotSchema{Count: len(snapshots), Results: snapshots})
	if err != nil {
		t.Fatalf("encode snapshot page: %v", err)
	}
	return string(body)
}

var (
	testPolicyID = core.UUID{6}
	testRegionID = core.UUID{7}
)

func sampleFilesystemSnapshotPolicy() *filestoragesdk.FilesystemSnapshotPolicySchema {
	return &filestoragesdk.FilesystemSnapshotPolicySchema{
		Id: testPolicyID, Name: "nightly", DaysOfWeek: []string{"mon", "fri"}, Time: "02:00:00", Retention: 1,
		Status: filestoragesdk.SnapshotPolicyStatusEnabled, State: filestoragesdk.SnapshotPolicyStateReady, FilesystemCount: 2,
		Region:    filestoragesdk.NestedRegionSchema{Id: testRegionID, Name: "vn-central-1"},
		OwnerId:   core.NullableFromPtr(&core.UUID{5}),
		ProjectId: testProjectID,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}
}

func filesystemSnapshotPolicyJSON(t *testing.T, policy *filestoragesdk.FilesystemSnapshotPolicySchema) string {
	t.Helper()
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("encode snapshot policy: %v", err)
	}
	return string(body)
}

func filesystemSnapshotPolicyPageJSON(t *testing.T, policies ...filestoragesdk.FilesystemSnapshotPolicySchema) string {
	t.Helper()
	body, err := json.Marshal(filestoragesdk.PagedFilesystemSnapshotPolicySchema{Count: len(policies), Results: policies})
	if err != nil {
		t.Fatalf("encode snapshot policy page: %v", err)
	}
	return string(body)
}
