package login

import (
	"embed"
	"io/fs"
)

//go:embed assets
var assetFiles embed.FS

// Asset returns the login pages' file at name ("assets/login.png"), served under /assets/.
func Asset(name string) ([]byte, error) {
	return fs.ReadFile(assetFiles, name)
}
