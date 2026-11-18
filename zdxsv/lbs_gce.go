package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/api/compute/v1"
)

// On-demand relay VMs on GCE (ZDXSV_GCE_ONDEMAND). The lobby keeps one VM per region, named
// zdxsv-relay-<region>-latest: a running one is reused, a stopped one is started again with a
// fresh startup script, else one is created in the first zone of the region that accepts it.
// A VM that stayed stopped for gceUnusedVMLifetime is deleted.
//
// The VMs are e2-micro in the dual-stack gceRelayNetwork, so they get an external IPv6 address,
// run as zdxsv-service@<project>.iam.gserviceaccount.com, download the lobby's own release,
// run its zdxsv only if it hashes like the lobby's executable, and connect back to
// ZDXSV_LOBBY_PUBLIC_ADDR with ZDXSV_RELAY_SECRET.

const (
	gceVersion          = "latest"
	gceRelayNetwork     = "zdxsv-relay"
	gceRelayNamePrefix  = "zdxsv-relay-"
	gceImage            = "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts"
	gceAllocInterval    = 30 * time.Second
	gceAllocTimeout     = 5 * time.Minute
	gceCleanupInterval  = time.Hour
	gceUnusedVMLifetime = 5 * 24 * time.Hour
)

var (
	gceAllocMu       sync.Mutex
	gceAllocTime     = map[string]time.Time{}
	gceCleanupTime   time.Time
	gceServiceMu     sync.Mutex
	gceServiceCached gceAPI
	relaySecretRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)
	relayTagRe       = regexp.MustCompile(`^v[0-9][0-9A-Za-z.+-]*$`)
	sha256HexRe      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// gcpRegionZones: the zones a VM may be created in, tried in order.
// https://cloud.google.com/compute/docs/regions-zones
var gcpRegionZones = map[string][]string{
	"asia-east1":              {"a", "b", "c"},
	"asia-east2":              {"a", "b", "c"},
	"asia-northeast1":         {"a", "b", "c"},
	"asia-northeast2":         {"a", "b", "c"},
	"asia-northeast3":         {"a", "b", "c"},
	"asia-southeast1":         {"a", "b", "c"},
	"australia-southeast1":    {"a", "b", "c"},
	"europe-west2":            {"a", "b", "c"},
	"northamerica-northeast1": {"a", "b", "c"},
	"southamerica-east1":      {"a", "b", "c"},
	"us-central1":             {"a", "b", "c", "f"},
	"us-east4":                {"a", "b", "c"},
	"us-west2":                {"a", "b", "c"},
}

// gceAPI is the part of the Compute Engine API the allocator uses; each call waits for
// its operation. gceFake in the tests implements it too.
type gceAPI interface {
	// listInstances lists the project's instances whose name starts with namePrefix.
	listInstances(ctx context.Context, namePrefix string) ([]*compute.Instance, error)
	getInstance(ctx context.Context, zone, name string) (*compute.Instance, error)
	setMetadata(ctx context.Context, zone, name string, metadata *compute.Metadata) error
	setScheduling(ctx context.Context, zone, name string, scheduling *compute.Scheduling) error
	startInstance(ctx context.Context, zone, name string) error
	insertInstance(ctx context.Context, zone string, instance *compute.Instance) error
	deleteInstance(ctx context.Context, zone, name string) error
}

type gceService struct {
	project string
	svc     *compute.Service
}

func (g *gceService) listInstances(ctx context.Context, namePrefix string) ([]*compute.Instance, error) {
	var list []*compute.Instance
	err := g.svc.Instances.AggregatedList(g.project).Filter(fmt.Sprintf("name eq %s.*", namePrefix)).
		Pages(ctx, func(page *compute.InstanceAggregatedList) error {
			for _, scoped := range page.Items {
				list = append(list, scoped.Instances...)
			}
			return nil
		})
	return list, err
}

func (g *gceService) getInstance(ctx context.Context, zone, name string) (*compute.Instance, error) {
	return g.svc.Instances.Get(g.project, zone, name).Context(ctx).Do()
}

func (g *gceService) setMetadata(ctx context.Context, zone, name string, metadata *compute.Metadata) error {
	op, err := g.svc.Instances.SetMetadata(g.project, zone, name, metadata).Context(ctx).Do()
	if err != nil {
		return err
	}
	return g.wait(ctx, zone, op)
}

func (g *gceService) setScheduling(ctx context.Context, zone, name string, scheduling *compute.Scheduling) error {
	op, err := g.svc.Instances.SetScheduling(g.project, zone, name, scheduling).Context(ctx).Do()
	if err != nil {
		return err
	}
	return g.wait(ctx, zone, op)
}

func (g *gceService) startInstance(ctx context.Context, zone, name string) error {
	op, err := g.svc.Instances.Start(g.project, zone, name).Context(ctx).Do()
	if err != nil {
		return err
	}
	return g.wait(ctx, zone, op)
}

func (g *gceService) insertInstance(ctx context.Context, zone string, instance *compute.Instance) error {
	op, err := g.svc.Instances.Insert(g.project, zone, instance).Context(ctx).Do()
	if err != nil {
		return err
	}
	return g.wait(ctx, zone, op)
}

func (g *gceService) deleteInstance(ctx context.Context, zone, name string) error {
	op, err := g.svc.Instances.Delete(g.project, zone, name).Context(ctx).Do()
	if err != nil {
		return err
	}
	return g.wait(ctx, zone, op)
}

func (g *gceService) wait(ctx context.Context, zone string, op *compute.Operation) error {
	for op.Status != "DONE" {
		var err error
		op, err = g.svc.ZoneOperations.Wait(g.project, zone, op.Name).Context(ctx).Do()
		if err != nil {
			return err
		}
	}
	if op.Error != nil && len(op.Error.Errors) > 0 {
		e := op.Error.Errors[0]
		return fmt.Errorf("%s: %s", e.Code, e.Message)
	}
	return nil
}

// getGceAPI uses the default credentials (GOOGLE_APPLICATION_CREDENTIALS).
func getGceAPI() (gceAPI, error) {
	gceServiceMu.Lock()
	defer gceServiceMu.Unlock()
	if gceServiceCached != nil {
		return gceServiceCached, nil
	}
	svc, err := compute.NewService(context.Background())
	if err != nil {
		return nil, err
	}
	gceServiceCached = &gceService{project: Conf.Lobby.GCPProjectID, svc: svc}
	return gceServiceCached, nil
}

func GceAllocEnabled() bool {
	return Conf.Lobby.GCEOnDemand && Conf.Lobby.GCPProjectID != ""
}

type GceRelayProvisioner struct{}

func (GceRelayProvisioner) StartRelay(region string) {
	GoGceAllocRelay(region)
}

// GoGceAllocRelay starts a relay VM in the region in the background, at most once per
// gceAllocInterval. It reports whether a request was made.
func GoGceAllocRelay(region string) bool {
	gceAllocMu.Lock()
	if time.Since(gceAllocTime[region]) <= gceAllocInterval {
		gceAllocMu.Unlock()
		return false
	}
	gceAllocTime[region] = time.Now()
	gceAllocMu.Unlock()

	go func() {
		api, err := getGceAPI()
		if err != nil {
			logger.Error("gce alloc: client", zap.Error(err))
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), gceAllocTimeout)
		defer cancel()
		vm, err := gceAlloc(ctx, api, gceAllocParams{
			Project:         Conf.Lobby.GCPProjectID,
			Region:          region,
			LobbyPublicAddr: Conf.Lobby.PublicAddr,
			RelaySecret:     string(Conf.Lobby.RelaySecret),
			Tag:             "v" + releaseVersion,
			BinarySHA256:    selfSHA256(),
		})
		if err != nil {
			logger.Error("gce alloc failed", zap.String("region", region), zap.Error(err))
			return
		}
		logger.Info("gce alloc", zap.String("region", region), zap.String("name", vm.Name),
			zap.String("zone", path.Base(vm.Zone)), zap.String("status", vm.Status))
	}()
	return true
}

type gceAllocParams struct {
	Project         string
	Region          string
	LobbyPublicAddr string
	RelaySecret     string
	Tag             string
	BinarySHA256    string
}

var selfSHA256 = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
})

func gceVMName(region string) string {
	return gceRelayNamePrefix + region + "-" + strings.ReplaceAll(gceVersion, ".", "-")
}

func gceServiceAccount(project string) string {
	return "zdxsv-service@" + project + ".iam.gserviceaccount.com"
}

func gceAlloc(ctx context.Context, api gceAPI, p gceAllocParams) (*compute.Instance, error) {
	zones, ok := gcpRegionZones[p.Region]
	if !ok {
		return nil, fmt.Errorf("invalid region %q", p.Region)
	}
	if !relaySecretRe.MatchString(p.RelaySecret) {
		return nil, errors.New("ZDXSV_RELAY_SECRET is not set or not plain [A-Za-z0-9_-]{16,}")
	}
	if p.LobbyPublicAddr == "" {
		return nil, errors.New("ZDXSV_LOBBY_PUBLIC_ADDR is empty")
	}
	if !relayTagRe.MatchString(p.Tag) || !sha256HexRe.MatchString(p.BinarySHA256) {
		return nil, fmt.Errorf("relays run the lobby's own release: not one (tag %q, sha256 %q)", p.Tag, p.BinarySHA256)
	}

	gceRemoveUnusedVMs(ctx, api)

	name := gceVMName(p.Region)
	startupScript := gceStartupScript(p)
	metadata := &compute.Metadata{Items: []*compute.MetadataItems{
		{Key: "startup-script", Value: &startupScript},
		{Key: "enable-osconfig", Value: strPtr("TRUE")},
		{Key: "enable-guest-attributes", Value: strPtr("TRUE")},
	}}
	scheduling := &compute.Scheduling{
		OnHostMaintenance: "MIGRATE",
		Preemptible:       false,
		AutomaticRestart:  boolPtr(false),
		ForceSendFields:   []string{"Preemptible", "AutomaticRestart"},
	}

	vms, err := api.listInstances(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	for _, vm := range vms {
		if vm.Name == name && vm.Status == "RUNNING" {
			return vm, nil
		}
	}

	for _, vm := range vms {
		if vm.Name != name || vm.Status != "TERMINATED" {
			continue
		}
		zone := path.Base(vm.Zone)
		logger.Info("gce alloc: starting stopped vm", zap.String("name", name), zap.String("zone", zone))
		if vm.Metadata != nil {
			metadata.Fingerprint = vm.Metadata.Fingerprint
		}
		if err := api.setMetadata(ctx, zone, name, metadata); err != nil {
			logger.Warn("gce alloc: set metadata", zap.String("zone", zone), zap.Error(err))
			continue
		}
		if err := api.setScheduling(ctx, zone, name, scheduling); err != nil {
			logger.Warn("gce alloc: set scheduling", zap.String("zone", zone), zap.Error(err))
			continue
		}
		if err := api.startInstance(ctx, zone, name); err != nil {
			logger.Warn("gce alloc: start", zap.String("zone", zone), zap.Error(err))
			continue
		}
		return api.getInstance(ctx, zone, name)
	}

	var lastErr error
	for _, z := range zones {
		zone := p.Region + "-" + z
		logger.Info("gce alloc: creating vm", zap.String("name", name), zap.String("zone", zone))
		inst := gceNewInstance(p, name, zone, metadata, scheduling)
		if err := api.insertInstance(ctx, zone, inst); err != nil {
			logger.Warn("gce alloc: insert", zap.String("zone", zone), zap.Error(err))
			lastErr = err
			continue
		}
		return api.getInstance(ctx, zone, name)
	}
	return nil, fmt.Errorf("failed to allocate vm in %s: %w", p.Region, lastErr)
}

func gceNewInstance(p gceAllocParams, name, zone string, metadata *compute.Metadata, scheduling *compute.Scheduling) *compute.Instance {
	return &compute.Instance{
		Name:        name,
		MachineType: "zones/" + zone + "/machineTypes/e2-micro",
		Tags:        &compute.Tags{Items: []string{"zdxsv-relay"}},
		Disks: []*compute.AttachedDisk{{
			Boot:             true,
			AutoDelete:       true,
			InitializeParams: &compute.AttachedDiskInitializeParams{SourceImage: gceImage},
		}},
		NetworkInterfaces: []*compute.NetworkInterface{{
			Network:           "global/networks/" + gceRelayNetwork,
			Subnetwork:        "regions/" + p.Region + "/subnetworks/" + gceRelayNetwork + "-" + p.Region,
			StackType:         "IPV4_IPV6",
			AccessConfigs:     []*compute.AccessConfig{{Type: "ONE_TO_ONE_NAT", Name: "External NAT", NetworkTier: "PREMIUM"}},
			Ipv6AccessConfigs: []*compute.AccessConfig{{Type: "DIRECT_IPV6", Name: "External IPv6", NetworkTier: "PREMIUM"}},
		}},
		Scheduling: scheduling,
		Metadata:   metadata,
		ServiceAccounts: []*compute.ServiceAccount{{
			Email:  gceServiceAccount(p.Project),
			Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
		}},
	}
}

// gceRemoveUnusedVMs deletes the relay VMs that have been stopped for gceUnusedVMLifetime,
// at most once per gceCleanupInterval.
func gceRemoveUnusedVMs(ctx context.Context, api gceAPI) {
	gceAllocMu.Lock()
	if time.Since(gceCleanupTime) < gceCleanupInterval {
		gceAllocMu.Unlock()
		return
	}
	gceCleanupTime = time.Now()
	gceAllocMu.Unlock()

	vms, err := api.listInstances(ctx, gceRelayNamePrefix)
	if err != nil {
		logger.Warn("gce cleanup: list", zap.Error(err))
		return
	}
	for _, vm := range vms {
		if vm.Status != "TERMINATED" || vm.LastStartTimestamp == "" {
			continue
		}
		last, err := time.Parse(time.RFC3339, vm.LastStartTimestamp)
		if err != nil || time.Since(last) < gceUnusedVMLifetime {
			continue
		}
		zone := path.Base(vm.Zone)
		logger.Info("gce cleanup: deleting unused vm", zap.String("name", vm.Name), zap.String("zone", zone))
		if err := api.deleteInstance(ctx, zone, vm.Name); err != nil {
			logger.Warn("gce cleanup: delete", zap.String("name", vm.Name), zap.Error(err))
		}
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func gceStartupScript(p gceAllocParams) string {
	return strings.NewReplacer(
		"__TAG__", p.Tag,
		"__BIN_SHA256__", p.BinarySHA256,
		"__LOBBY_PUBLIC_ADDR__", p.LobbyPublicAddr,
		"__RELAY_SECRET__", p.RelaySecret,
	).Replace(gceRelayStartupScript)
}

const gceRelayStartupScript = `#!/bin/bash
echo "startup-script"

cat << 'EOF' > /home/ubuntu/launch-relay.sh
#!/bin/bash -eux

# The lobby stops the relay once no battle needs it; stop the VM with it.
function finish {
  echo "relay finished" | logger
  sleep 1
  sudo /sbin/shutdown now
}
trap finish EXIT

readonly REPO=inada-s/zdxsv
readonly TAG_NAME=__TAG__
curl=(curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors --max-time 120)
readonly ASSET="zdxsv-server_${TAG_NAME#v}_linux-x86_64.tar.gz"
readonly URL="https://github.com/$REPO/releases/download/$TAG_NAME"

if [[ ! -x $TAG_NAME/zdxsv ]]; then
  echo "Downloading $TAG_NAME"
  mkdir -p "$TAG_NAME"
  pushd "$TAG_NAME"
    "${curl[@]}" -o "$ASSET" "$URL/$ASSET"
    "${curl[@]}" -o checksums.txt "$URL/checksums.txt"
    grep " $ASSET\$" checksums.txt | sha256sum -c -
    tar --no-same-owner -xzf "$ASSET" && rm "$ASSET"
  popd
fi
echo "__BIN_SHA256__  $TAG_NAME/zdxsv" | sha256sum -c -

readonly METADATA=http://metadata.google.internal/computeMetadata/v1/instance
readonly ZONE=$(basename $(curl -s -H "Metadata-Flavor: Google" $METADATA/zone))
export ZDXSV_LOBBY_PUBLIC_ADDR=__LOBBY_PUBLIC_ADDR__
export ZDXSV_RELAY_ADDR=:8223
export ZDXSV_RELAY_SECRET=__RELAY_SECRET__
export ZDXSV_RELAY_REGION=${ZONE%-*}
export ZDXSV_RELAY_PUBLIC_ADDR=$(curl -s -H "Metadata-Flavor: Google" $METADATA/network-interfaces/0/access-configs/0/external-ip):8223
# The VM's external IPv6 address is on its interface. Skip temporary and deprecated ones.
readonly IPV6=$(ip -6 -o addr show scope global | grep -v -e temporary -e deprecated | awk '{print $4}' | cut -d/ -f1 | head -n1)
if [[ -n $IPV6 ]]; then
  export ZDXSV_RELAY_PUBLIC_ADDR6="[$IPV6]:8223"
fi

"$TAG_NAME"/zdxsv -prodlog relay >> /var/log/zdxsv-relay.log 2>&1
EOF

touch /var/log/zdxsv-relay.log
truncate -s0 /var/log/zdxsv-relay.log
chown ubuntu:ubuntu /var/log/zdxsv-relay.log

chmod +x /home/ubuntu/launch-relay.sh

su ubuntu -c 'cd /home/ubuntu && nohup ./launch-relay.sh &'
echo "startup-script done"
`
