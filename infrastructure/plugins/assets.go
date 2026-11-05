// Package pluginassets embeds plugin packages distributed with the platform.
package pluginassets

import _ "embed"

//go:embed openviking/openviking-dsh-memory-plugin-0.5.31.tgz
var OpenVikingPlugin []byte

//go:embed mem0-dist/harness-cloud-dsh-mem0-memory-0.2.0.tgz
var Mem0Plugin []byte

//go:embed graphiti-dist/harness-cloud-dsh-graphiti-memory-0.2.0.tgz
var GraphitiPlugin []byte
