// Package rules exposes the vendored Beacon rule files as an embedded filesystem
// for Threadforge's explicit, opt-in local rule-pack installation.
package rules

import "embed"

// Files contains the exact MIT-licensed rule corpus at the vendored Beacon pin.
//
//go:embed */*.rule.yaml
var Files embed.FS
