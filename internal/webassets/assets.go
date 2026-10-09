package webassets

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var content embed.FS

func Files() fs.FS {
	f, err := fs.Sub(content, "dist")
	if err != nil {
		panic(err)
	}
	return f
}
