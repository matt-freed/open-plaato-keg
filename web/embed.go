// Package web holds the embedded browser UI.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFiles embed.FS

// Static returns the UI's file system, rooted at the directory served at "/".
func Static() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		// The embed directive guarantees this directory exists.
		panic("web: embedded static directory is missing: " + err.Error())
	}
	return sub
}
