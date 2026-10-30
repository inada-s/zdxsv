package lobby

import (
	"strings"

	"zdxsv/pkg/db"

	"github.com/blang/semver"
	"github.com/golang/glog"
)

// minPcsx2VersionKey: m_server_setting key of the lowest pcsx2 release (zdxsv-X.Y.Z) that may log in.
// Read at each login, so a change in the db applies at once.
const minPcsx2VersionKey = "min_pcsx2_version"

func parsePcsx2Version(v string) (semver.Version, error) {
	return semver.Parse(strings.TrimPrefix(v, "zdxsv-"))
}

// pcsx2NeedsUpdate: the client sent its pcsx2 version (platform info "pcsx2") and it is below the minimum
// or not a version at all. No version sent (consoles, builds without a release tag) or no minimum: false.
func pcsx2NeedsUpdate(version string) bool {
	if version == "" {
		return false
	}
	minVersion, err := db.DefaultDB.GetServerSetting(minPcsx2VersionKey)
	if err != nil {
		glog.Warningln("GetServerSetting failure", minPcsx2VersionKey, err)
		return false
	}
	if minVersion == "" {
		return false
	}
	min, err := parsePcsx2Version(minVersion)
	if err != nil {
		glog.Warningln("invalid", minPcsx2VersionKey, minVersion, err)
		return false
	}
	v, err := parsePcsx2Version(version)
	return err != nil || v.LT(min)
}
