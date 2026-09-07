//go:build !linux

package agent

// Run rejects non-Linux platforms. This helper permits portable unit tests.
func preserveOwner(source, destination string) error { return nil }
