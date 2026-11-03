// Package workspaceassets embeds scripts executed in the workspace runtime.
package workspaceassets

import _ "embed"

//go:embed install_skills.mjs
var InstallSkills string

//go:embed install_extensions.mjs
var InstallExtensions string

//go:embed cloud_extensions.mjs
var CloudExtensions string

//go:embed openviking_session.mjs
var OpenVikingSession string
