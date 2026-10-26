// Package cliout shapes command output: it resolves the output mode from the
// --format/--quiet flags and renders lists and prune reports as tables,
// identifiers, or JSON. It holds no cobra wiring, so formatters are tested
// without building commands.
package cliout
