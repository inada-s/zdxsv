package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/compute/v1"
)

// gceFake is an in-memory Compute Engine: instances by zone/name, and a log of the calls.
type gceFake struct {
	instances []*compute.Instance
	calls     []string
	insertErr map[string]error // zone -> error
}

func (f *gceFake) find(zone, name string) *compute.Instance {
	for _, i := range f.instances {
		if i.Name == name && strings.HasSuffix(i.Zone, "/"+zone) {
			return i
		}
	}
	return nil
}

func (f *gceFake) listInstances(_ context.Context, prefix string) ([]*compute.Instance, error) {
	f.calls = append(f.calls, "list "+prefix)
	var out []*compute.Instance
	for _, i := range f.instances {
		if strings.HasPrefix(i.Name, prefix) {
			out = append(out, i)
		}
	}
	return out, nil
}

func (f *gceFake) getInstance(_ context.Context, zone, name string) (*compute.Instance, error) {
	if i := f.find(zone, name); i != nil {
		return i, nil
	}
	return nil, errors.New("not found")
}

func (f *gceFake) setMetadata(_ context.Context, zone, name string, m *compute.Metadata) error {
	f.calls = append(f.calls, "metadata "+zone+"/"+name)
	i := f.find(zone, name)
	if i == nil {
		return errors.New("not found")
	}
	i.Metadata = m
	return nil
}

func (f *gceFake) setScheduling(_ context.Context, zone, name string, s *compute.Scheduling) error {
	f.calls = append(f.calls, "scheduling "+zone+"/"+name)
	f.find(zone, name).Scheduling = s
	return nil
}

func (f *gceFake) startInstance(_ context.Context, zone, name string) error {
	f.calls = append(f.calls, "start "+zone+"/"+name)
	f.find(zone, name).Status = "RUNNING"
	return nil
}

func (f *gceFake) insertInstance(_ context.Context, zone string, inst *compute.Instance) error {
	f.calls = append(f.calls, "insert "+zone+"/"+inst.Name)
	if err := f.insertErr[zone]; err != nil {
		return err
	}
	inst.Zone = "https://www.googleapis.com/compute/v1/projects/p/zones/" + zone
	inst.Status = "RUNNING"
	f.instances = append(f.instances, inst)
	return nil
}

func (f *gceFake) deleteInstance(_ context.Context, zone, name string) error {
	f.calls = append(f.calls, "delete "+zone+"/"+name)
	for n, i := range f.instances {
		if i == f.find(zone, name) {
			f.instances = append(f.instances[:n], f.instances[n+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

func fakeInstance(zone, name, status string) *compute.Instance {
	return &compute.Instance{
		Name:     name,
		Zone:     "https://www.googleapis.com/compute/v1/projects/p/zones/" + zone,
		Status:   status,
		Metadata: &compute.Metadata{Fingerprint: "fp"},
	}
}

func metadataValue(m *compute.Metadata, key string) string {
	for _, it := range m.Items {
		if it.Key == key && it.Value != nil {
			return *it.Value
		}
	}
	return ""
}

func resetGceState() {
	gceAllocMu.Lock()
	gceCleanupTime = time.Time{}
	gceAllocTime = map[string]time.Time{}
	gceAllocMu.Unlock()
}

const testSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var testAllocParams = gceAllocParams{
	Project:         "proj",
	Region:          "asia-east2",
	LobbyPublicAddr: "153.121.44.150:8200",
	RelaySecret:     "0123456789abcdef_relay",
	Tag:             "v1.2.3",
	BinarySHA256:    testSHA256,
}

func TestGceAllocReusesRunningVM(t *testing.T) {
	resetGceState()
	f := &gceFake{instances: []*compute.Instance{fakeInstance("asia-east2-b", "zdxsv-relay-asia-east2-latest", "RUNNING")}}
	vm, err := gceAlloc(context.Background(), f, testAllocParams)
	if err != nil {
		t.Fatal(err)
	}
	if vm.Name != "zdxsv-relay-asia-east2-latest" || strings.Join(f.calls, ",") != "list zdxsv-relay-,list zdxsv-relay-asia-east2-latest" {
		t.Errorf("vm %s calls %v", vm.Name, f.calls)
	}
}

func TestGceAllocStartsStoppedVM(t *testing.T) {
	resetGceState()
	f := &gceFake{instances: []*compute.Instance{
		fakeInstance("asia-east2-a", "zdxsv-relay-asia-east2-latest-old", "TERMINATED"), // another name: left alone
		fakeInstance("asia-east2-c", "zdxsv-relay-asia-east2-latest", "TERMINATED"),
	}}
	vm, err := gceAlloc(context.Background(), f, testAllocParams)
	if err != nil {
		t.Fatal(err)
	}
	want := "metadata asia-east2-c/zdxsv-relay-asia-east2-latest,scheduling asia-east2-c/zdxsv-relay-asia-east2-latest,start asia-east2-c/zdxsv-relay-asia-east2-latest"
	if got := strings.Join(f.calls[2:], ","); got != want || vm.Status != "RUNNING" {
		t.Errorf("calls %v status %s", f.calls, vm.Status)
	}
	if vm.Metadata.Fingerprint != "fp" || !strings.Contains(metadataValue(vm.Metadata, "startup-script"), "ZDXSV_LOBBY_PUBLIC_ADDR=153.121.44.150:8200") {
		t.Errorf("metadata %+v", vm.Metadata)
	}
	if vm.Scheduling.OnHostMaintenance != "MIGRATE" || vm.Scheduling.Preemptible || *vm.Scheduling.AutomaticRestart {
		t.Errorf("scheduling %+v", vm.Scheduling)
	}
}

func TestGceAllocCreatesVM(t *testing.T) {
	resetGceState()
	f := &gceFake{insertErr: map[string]error{"asia-east2-a": errors.New("ZONE_RESOURCE_POOL_EXHAUSTED")}}
	vm, err := gceAlloc(context.Background(), f, testAllocParams)
	if err != nil {
		t.Fatal(err)
	}
	nic := vm.NetworkInterfaces[0]
	if vm.Name != "zdxsv-relay-asia-east2-latest" || !strings.HasSuffix(vm.Zone, "/asia-east2-b") || vm.MachineType != "zones/asia-east2-b/machineTypes/e2-micro" || vm.Tags.Items[0] != "zdxsv-relay" {
		t.Errorf("vm %s zone %s type %s tags %v", vm.Name, vm.Zone, vm.MachineType, vm.Tags.Items)
	}
	if nic.Network != "global/networks/zdxsv-relay" || nic.Subnetwork != "regions/asia-east2/subnetworks/zdxsv-relay-asia-east2" || nic.StackType != "IPV4_IPV6" || len(nic.Ipv6AccessConfigs) != 1 {
		t.Errorf("nic %+v", nic)
	}
	if vm.ServiceAccounts[0].Email != "zdxsv-service@proj.iam.gserviceaccount.com" || !vm.Disks[0].Boot || vm.Disks[0].InitializeParams.SourceImage != gceImage {
		t.Errorf("sa %v disks %+v", vm.ServiceAccounts, vm.Disks[0])
	}
	script := metadataValue(vm.Metadata, "startup-script")
	for _, want := range []string{"readonly TAG_NAME=v1.2.3", "echo \"" + testSHA256 + "  $TAG_NAME/zdxsv\" | sha256sum -c -", "zdxsv-server_${TAG_NAME#v}_linux-x86_64.tar.gz", "sha256sum -c -",
		"ZDXSV_RELAY_SECRET=0123456789abcdef_relay", "ZDXSV_LOBBY_PUBLIC_ADDR=153.121.44.150:8200", "ZDXSV_RELAY_ADDR=:8223",
		"ZDXSV_RELAY_REGION=${ZONE%-*}", "zdxsv -prodlog relay"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(script, "__") {
		t.Errorf("script keeps a placeholder: %s", script)
	}
}

func TestGceAllocRejectsBadParams(t *testing.T) {
	resetGceState()
	f := &gceFake{}
	for _, p := range []gceAllocParams{
		{Project: "p", Region: "mars-1", LobbyPublicAddr: "a:1", RelaySecret: "0123456789abcdef_relay", Tag: "v1.2.3", BinarySHA256: testSHA256},
		{Project: "p", Region: "asia-east2", RelaySecret: "0123456789abcdef_relay", Tag: "v1.2.3", BinarySHA256: testSHA256},
		{Project: "p", Region: "asia-east2", LobbyPublicAddr: "a:1", RelaySecret: "short", Tag: "v1.2.3", BinarySHA256: testSHA256},
		{Project: "p", Region: "asia-east2", LobbyPublicAddr: "a:1", RelaySecret: "0123456789abcdef_relay", Tag: "vlocal", BinarySHA256: testSHA256},
		{Project: "p", Region: "asia-east2", LobbyPublicAddr: "a:1", RelaySecret: "0123456789abcdef_relay", Tag: "v1.2.3; reboot", BinarySHA256: testSHA256},
		{Project: "p", Region: "asia-east2", LobbyPublicAddr: "a:1", RelaySecret: "0123456789abcdef_relay", Tag: "v1.2.3"},
	} {
		if _, err := gceAlloc(context.Background(), f, p); err == nil {
			t.Errorf("%+v allocated", p)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("calls %v", f.calls)
	}
}

func TestGceRemoveUnusedVMs(t *testing.T) {
	resetGceState()
	old := time.Now().Add(-6 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	f := &gceFake{instances: []*compute.Instance{
		fakeInstance("asia-east2-a", "zdxsv-relay-asia-east2-latest", "TERMINATED"),
		fakeInstance("us-west2-b", "zdxsv-relay-us-west2-latest", "TERMINATED"),
		fakeInstance("us-east4-b", "zdxsv-relay-us-east4-latest", "RUNNING"),
		fakeInstance("us-east4-b", "other-vm", "TERMINATED"),
	}}
	f.instances[0].LastStartTimestamp = recent
	f.instances[1].LastStartTimestamp = old
	f.instances[2].LastStartTimestamp = old
	f.instances[3].LastStartTimestamp = old

	gceRemoveUnusedVMs(context.Background(), f)
	var deleted []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "delete ") {
			deleted = append(deleted, c)
		}
	}
	if strings.Join(deleted, ",") != "delete us-west2-b/zdxsv-relay-us-west2-latest" {
		t.Errorf("deleted %v", deleted)
	}

	// once per gceCleanupInterval
	f.calls = nil
	gceRemoveUnusedVMs(context.Background(), f)
	if len(f.calls) != 0 {
		t.Errorf("second cleanup made calls %v", f.calls)
	}
}
