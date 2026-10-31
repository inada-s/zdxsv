package lobby

import (
	"sort"
	"strconv"
	"sync"
)

// maxRelaysPerBattle is the number of relay_<k> lines a client accepts.
const maxRelaysPerBattle = 4

// relayUnreachableMs is the relayed RTT of a player pair when either has no latency for the relay's region.
const relayUnreachableMs = 999

// RelaySessions issues the token of a battle's relay session; the same id gives the same token.
type RelaySessions interface {
	SessionToken(id uint32) uint64
}

// RegionalRelay is a relay in a cloud region, registered with the lobby. Region is a
// "<region>=<ms>" platform info key.
type RegionalRelay struct {
	Region      string
	PublicAddr  string
	PublicAddr6 string
	Sessions    RelaySessions

	// set by the relay control connection, under regionalRelays
	activeSessions int
	shutdown       func()
}

var regionalRelays = struct {
	sync.Mutex
	byAddr map[string]*RegionalRelay
}{byAddr: map[string]*RegionalRelay{}}

// RegisterRelay offers r to battles until UnregisterRelay(r.PublicAddr); a relay with the same
// PublicAddr is replaced.
func RegisterRelay(r *RegionalRelay) {
	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	regionalRelays.byAddr[r.PublicAddr] = r
}

func UnregisterRelay(publicAddr string) {
	regionalRelays.Lock()
	defer regionalRelays.Unlock()
	delete(regionalRelays.byAddr, publicAddr)
}

// battleRelays lists the relays offered to a battle of users, best first: regional relays by
// the worst relayed RTT of a player pair (the sum of both players' latencies to the relay's
// region), then the lobby's own relay, at most maxRelaysPerBattle. Clients settle on the
// earlier relay when they disagree, so the order depends only on the battle, never on the
// player asking.
func battleRelays(users []string, info func(userID string) map[string]string) []*RegionalRelay {
	latency := func(id, region string) int {
		ms, err := strconv.Atoi(info(id)[region])
		if err != nil || ms <= 0 {
			return 0
		}
		return ms
	}
	type scored struct {
		r     *RegionalRelay
		worst int
	}
	var list []scored
	regionalRelays.Lock()
	for _, r := range regionalRelays.byAddr {
		worst := 0
		for i := range users {
			for j := i + 1; j < len(users); j++ {
				a, b := latency(users[i], r.Region), latency(users[j], r.Region)
				via := relayUnreachableMs
				if a > 0 && b > 0 {
					via = a + b
				}
				worst = max(worst, via)
			}
		}
		list = append(list, scored{r, worst})
	}
	regionalRelays.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].worst != list[j].worst {
			return list[i].worst < list[j].worst
		}
		return list[i].r.PublicAddr < list[j].r.PublicAddr
	})
	var relays []*RegionalRelay
	for _, s := range list {
		relays = append(relays, s.r)
	}
	if LobbyRelay != nil && RelayPublicAddr != "" {
		relays = append(relays, &RegionalRelay{PublicAddr: RelayPublicAddr, PublicAddr6: RelayPublicAddr6, Sessions: LobbyRelay})
	}
	if len(relays) > maxRelaysPerBattle {
		relays = relays[:maxRelaysPerBattle]
	}
	return relays
}
