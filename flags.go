package main

import "flag"

// These thin wrappers keep registerFlags() readable. They register on the
// default flag.CommandLine, so flag.Parse() in main() drives them.

func strPtr(name, def, usage string) *string         { return flag.String(name, def, usage) }
func intPtr(name string, def int, usage string) *int { return flag.Int(name, def, usage) }
func floatPtr(name string, def float64, usage string) *float64 {
	return flag.Float64(name, def, usage)
}
func boolPtr(name string, def bool, usage string) *bool { return flag.Bool(name, def, usage) }
