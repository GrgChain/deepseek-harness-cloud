// Package pluginassets embeds plugin packages distributed with the platform.
package pluginassets

import _ "embed"

//go:embed openviking/openviking-dsh-memory-plugin-0.5.31.tgz
var OpenVikingPlugin []byte
