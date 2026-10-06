// Package bundlespec embeds the public bundle schemas so validation is offline
// and uses the same definitions distributed to independent implementations.
package bundlespec

import "embed"

//go:embed spec/*.schema.json bundle-evidence.schema.json
var Schemas embed.FS
