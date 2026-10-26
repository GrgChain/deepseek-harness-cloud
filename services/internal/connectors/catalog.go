package connectors

import (
	_ "embed"
	"encoding/json"
)

// Catalog metadata is bundled for offline use. It grants no network access and
// does not install or execute external adapters. Metadata attribution is in NOTICE.
//
//go:embed catalog.json
var catalogJSON []byte

type CatalogEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Category     string   `json:"category"`
	Mode         string   `json:"mode"`
	AuthKind     string   `json:"auth_kind"`
	DocURL       string   `json:"doc_url"`
	GuideURL     string   `json:"guide_url"`
	SetupHint    string   `json:"setup_hint"`
	Requirements []string `json:"requirements"`
	Color        string   `json:"color"`
}

type Catalog struct {
	Version   int            `json:"version"`
	SourceURL string         `json:"source_url"`
	Entries   []CatalogEntry `json:"entries"`
}

func builtinCatalog() Catalog {
	var catalog Catalog
	// Versioned application data; catalog completeness is checked by tests.
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic("invalid built-in connector catalog")
	}
	return catalog
}

func knownCatalogID(id string) bool {
	for _, entry := range builtinCatalog().Entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}
