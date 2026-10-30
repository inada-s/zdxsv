package lobby

import (
	"context"
	"os/exec"
	"strconv"
	"time"

	"github.com/golang/glog"
)

const (
	relayMinUsers     = 4
	relayIdleShutdown = 30 * time.Minute
	// relayStartRetry is how long a started region may take to register before it is started again.
	relayStartRetry   = 10 * time.Minute
	relayStartTimeout = 5 * time.Minute
)

// relayRegionGroup is the area of each "<region>=<ms>" key players report; players whose best
// regions are in different groups may play across a long route.
var relayRegionGroup = map[string]string{
	"asia-east1":              "asia-east",
	"asia-east2":              "asia-east",
	"asia-northeast1":         "asia-northeast",
	"asia-northeast2":         "asia-northeast",
	"asia-northeast3":         "asia-northeast",
	"asia-southeast1":         "asia-southeast",
	"australia-southeast1":    "australia-southeast",
	"europe-west2":            "europe-west",
	"northamerica-northeast1": "northamerica-northeast",
	"southamerica-east1":      "southamerica-east",
	"us-central1":             "us-central",
	"us-east4":                "us-east",
	"us-west2":                "us-west",
}

// RelayProvisioner starts a regional relay (a VM, a container) in a region; the relay then
// registers through the relay control address. StartRelay must not block the lobby.
type RelayProvisioner interface {
	StartRelay(region string)
}

// CommandProvisioner runs Command with the region as its only argument.
type CommandProvisioner struct {
	Command string
}

func (c CommandProvisioner) StartRelay(region string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), relayStartTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, c.Command, region).CombinedOutput()
		if err != nil {
			glog.Errorln("relay start failed", region, err, string(out))
			return
		}
		glog.Infoln("relay start", region, string(out))
	}()
}

type relayProvisioning struct {
	regions    []string
	prov       RelayProvisioner
	lastNeeded map[string]time.Time
	lastStart  map[string]time.Time
}

// SetRelayProvisioning makes the lobby start a relay in each of regions while relays may be
// needed, and shut down a relay of these regions once unneeded for relayIdleShutdown with no
// sessions. Call before Serve.
func (a *App) SetRelayProvisioning(regions []string, prov RelayProvisioner) {
	a.relayProv = &relayProvisioning{
		regions:    regions,
		prov:       prov,
		lastNeeded: map[string]time.Time{},
		lastStart:  map[string]time.Time{},
	}
}

// relayNeeded reports whether a battle's worth of relay-capable players is online from at
// least two region groups.
func (a *App) relayNeeded() bool {
	users := 0
	first, mixed := "", false
	for _, p := range a.users {
		if p.PlatformInfo["relay_server"] != "1" {
			continue
		}
		users++
		g := relayRegionGroup[bestRelayRegion(p.PlatformInfo)]
		if g == "" {
			continue
		}
		if first == "" {
			first = g
		} else if g != first {
			mixed = true
		}
	}
	return users >= relayMinUsers && mixed
}

func bestRelayRegion(info map[string]string) string {
	best, bestMs := "", 0
	for region := range relayRegionGroup {
		ms, err := strconv.Atoi(info[region])
		if err != nil || ms <= 0 {
			continue
		}
		if best == "" || ms < bestMs || ms == bestMs && region < best {
			best, bestMs = region, ms
		}
	}
	return best
}

func (a *App) updateRelays(now time.Time) {
	rp := a.relayProv
	if rp == nil {
		return
	}
	regionalRelays.Lock()
	running := map[string]bool{}
	for _, r := range regionalRelays.byAddr {
		running[r.Region] = true
	}
	regionalRelays.Unlock()

	needed := a.relayNeeded()
	for _, region := range rp.regions {
		if needed {
			rp.lastNeeded[region] = now
		} else if _, ok := rp.lastNeeded[region]; !ok && running[region] {
			// a relay already running when the lobby started gets the full grace period
			rp.lastNeeded[region] = now
		}
		if needed && !running[region] && now.Sub(rp.lastStart[region]) >= relayStartRetry {
			rp.lastStart[region] = now
			glog.Infoln("relay needed, starting one in", region)
			rp.prov.StartRelay(region)
		}
	}

	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	for _, r := range regionalRelays.byAddr {
		last, ok := rp.lastNeeded[r.Region]
		if !ok || now.Sub(last) < relayIdleShutdown || r.activeSessions != 0 || r.shutdown == nil {
			continue
		}
		glog.Infoln("relay no longer needed, shutting it down", r.Region, r.PublicAddr)
		r.shutdown()
		r.shutdown = nil
	}
}
