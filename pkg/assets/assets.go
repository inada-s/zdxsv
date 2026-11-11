// Package assets holds the login pages' templates and images, served under /assets/.
package assets

import (
	"embed"
	"io/fs"
)

//go:embed assets
var files embed.FS

// Asset returns the file at name ("assets/login.png").
func Asset(name string) ([]byte, error) {
	return fs.ReadFile(files, name)
}
