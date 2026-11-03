// Package dshadapters embeds browser compatibility adapters for the gateway.
package dshadapters

import _ "embed"

//go:embed managed-models-read.js
var ManagedModelsRead string

//go:embed managed-models-view.js
var ManagedModelsView string

//go:embed managed-model-catalog-read.js
var ManagedModelCatalogRead string

//go:embed managed-directory.js
var ManagedDirectory string
