// Package authassets embeds the browser-side login behavior.
package authassets

import _ "embed"

//go:embed login.html
var HTML string

//go:embed login.css
var CSS string

//go:embed login.js
var Login string
