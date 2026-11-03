// Package workerassets embeds programs executed by isolated storage helpers.
package workerassets

import _ "embed"

//go:embed inventory.py
var Inventory string

//go:embed asset_operations.py
var AssetOperations string

//go:embed storage_loop.py
var StorageLoop string

//go:embed storage_main.py
var StorageMain string

//go:embed cleanup_fixture.py
var TestCleanup string

//go:embed partial_restore_fixture.py
var TestPartialRestore string
