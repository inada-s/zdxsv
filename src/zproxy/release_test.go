package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// goreleaserArchives: archive id -> name_template ("" = goreleaser's default) from .goreleaser.yml.
func goreleaserArchives(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("../../.goreleaser.yml")
	if err != nil {
		t.Fatal(err)
	}
	archives := map[string]string{}
	section, id := "", ""
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-") {
			section, id = strings.TrimSuffix(strings.TrimSpace(line), ":"), ""
			continue
		}
		if section != "archives" {
			continue
		}
		key, val, _ := strings.Cut(strings.TrimLeft(line, " -"), ":")
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch key {
		case "id":
			id = val
			archives[id] = ""
		case "name_template":
			archives[id] = val
		}
	}
	if len(archives) == 0 {
		t.Fatal("no archives found in .goreleaser.yml")
	}
	return archives
}

// TestReleaseAssetNames: zproxy's self-update (go-github-selfupdate) takes, from the newest
// release, any asset whose name ends in <os>_<arch> or <os>-<arch> plus an archive extension,
// and old zproxy versions apply no filter. So the zproxy archive must keep goreleaser's default
// name (zdxsv_<ver>_<os>_<arch>.zip) and no other archive may end that way.
func TestReleaseAssetNames(t *testing.T) {
	archives := goreleaserArchives(t)
	if tpl, ok := archives["zproxy"]; !ok || tpl != "" {
		t.Fatalf("zproxy archive must use the default name_template, got %q (present: %v)", tpl, ok)
	}
	vars := regexp.MustCompile(`{{\s*\.(\w+)\s*}}`)
	for id, tpl := range archives {
		if id == "zproxy" {
			continue
		}
		if tpl == "" {
			t.Errorf("archive %s has no name_template: the default one looks like zproxy's", id)
			continue
		}
		name := vars.ReplaceAllStringFunc(tpl, func(m string) string {
			switch vars.FindStringSubmatch(m)[1] {
			case "Version":
				return "1.2.3"
			case "Os":
				return "linux"
			case "Arch":
				return "amd64"
			}
			return "x"
		})
		for _, os := range []string{"linux", "darwin", "windows", "freebsd"} {
			for _, arch := range []string{"386", "amd64", "arm", "arm64"} {
				for _, sep := range []string{"_", "-"} {
					for _, ext := range []string{"", ".exe"} {
						if suffix := fmt.Sprintf("%s%s%s%s", os, sep, arch, ext); strings.HasSuffix(name, suffix) {
							t.Errorf("archive %s: %q ends in %q, which zproxy's self-update would take as its own", id, name, suffix)
						}
					}
				}
			}
		}
	}
}
