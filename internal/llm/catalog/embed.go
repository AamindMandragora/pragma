package catalog

import "embed"

//go:embed data/*.json
var dataFS embed.FS
