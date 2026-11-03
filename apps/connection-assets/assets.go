// Package connectionassets embeds the workspace connection page.
package connectionassets

import _ "embed"

//go:embed connection.html
var HTML string

//go:embed connection.css
var CSS string
