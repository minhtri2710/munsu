package contract

import (
	"embed"
	"io/fs"
)

//go:embed fixtures/*.json fixtures/*.toon
var embeddedFixtures embed.FS

var fixtures fs.FS = embeddedFixtures
