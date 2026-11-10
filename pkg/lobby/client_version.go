package lobby

import (
	"strings"

	"zdxsv/pkg/db"

	"github.com/blang/semver"
	"go.uber.org/zap"
	"zdxsv/pkg/zlog"
)

// minPcsx2VersionKey: m_server_setting key of the lowest pcsx2 release (zdxsv-X.Y.Z) that may log in.
// Set, it also refuses emulators that send no release version (builds from source, dev builds).
// Read at each login, so a change in the db applies at once.
const minPcsx2VersionKey = "min_pcsx2_version"

// pcsx2AllowKey: m_server_setting key of login keys and machine ids, separated by spaces or commas,
// that skip the version check (development builds on a gated server).
const pcsx2AllowKey = "pcsx2_allow"

func parsePcsx2Version(v string) (semver.Version, error) {
	return semver.Parse(strings.TrimPrefix(v, "zdxsv-"))
}

func pcsx2Allowed(loginKey, machineID string) bool {
	list, err := db.DefaultDB.GetServerSetting(pcsx2AllowKey)
	if err != nil {
		zlog.Warn("GetServerSetting failure", zap.Any("key", pcsx2AllowKey), zap.Error(err))
		return false
	}
	for _, v := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if v == loginKey || (machineID != "" && v == machineID) {
			return true
		}
	}
	return false
}

// pcsx2NeedsUpdate: with a minimum set, an emulator must send a release version (platform info "pcsx2",
// zdxsv-X.Y.Z without a pre-release part) not below it. Consoles and an unset minimum: false.
func pcsx2NeedsUpdate(info map[string]string, loginKey, machineID string) bool {
	if info["emulator"] == "" {
		return false
	}
	minVersion, err := db.DefaultDB.GetServerSetting(minPcsx2VersionKey)
	if err != nil {
		zlog.Warn("GetServerSetting failure", zap.Any("key", minPcsx2VersionKey), zap.Error(err))
		return false
	}
	if minVersion == "" {
		return false
	}
	min, err := parsePcsx2Version(minVersion)
	if err != nil {
		zlog.Warn("invalid server setting", zap.Any("key", minPcsx2VersionKey), zap.Any("value", minVersion), zap.Error(err))
		return false
	}
	v, err := parsePcsx2Version(info["pcsx2"])
	if err == nil && len(v.Pre) == 0 && v.GE(min) {
		return false
	}
	return !pcsx2Allowed(loginKey, machineID)
}
