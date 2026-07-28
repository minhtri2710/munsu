package cli

import (
	"embed"
	"io/fs"
)

//go:embed contract_fixtures/*.json contract_fixtures/*.toon
var embeddedFixtures embed.FS

var fixtures fs.FS = embeddedFixtures
