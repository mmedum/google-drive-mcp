package main

import (
	"sort"

	"github.com/mmedum/google-drive-mcp/v3/internal/config"
)

// fullSurfaceEnv turns on every feature that gates a tool, so a dump
// carries the whole registrable surface rather than a default build's.
//
// It is every switch config.Vars records but read-only, rather than a
// list here: a list written from memory falls behind the moment a phase
// adds a flag, and the gate that was supposed to notice goes quiet
// instead of failing. Every other switch registers tools. GDRIVE_READ_ONLY
// REMOVES them, so turning it on would shrink the surface it is meant to
// widen. A setting that is not a switch only changes behavior: setting
// GDRIVE_SHARING or GDRIVE_LOCAL_DIR to "true" would be nonsense.
func fullSurfaceEnv() []string {
	var env []string
	for _, v := range config.Vars() {
		if v.Switch && v.Name != config.EnvPrefix+"READ_ONLY" {
			env = append(env, v.Name+"=true")
		}
	}
	sort.Strings(env)
	return env
}
