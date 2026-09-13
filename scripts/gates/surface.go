package main

import "sort"

// fullSurfaceEnv turns on every feature that gates a tool, so a dump
// carries the whole registrable surface rather than a default build's.
//
// It is derived from config.go's own Define calls rather than listed
// here, for the reason checkConfigDocs is derived: a list written from
// memory falls behind the moment a phase adds a flag, and the gate that
// was supposed to notice goes quiet instead of failing. GDRIVE_READ_ONLY
// is the one boolean left off — it REMOVES tools rather than adding
// them, so turning it on would shrink the surface it is meant to widen.
func fullSurfaceEnv() ([]string, error) {
	settings, err := definedSettings()
	if err != nil {
		return nil, err
	}
	var env []string
	for _, name := range settings {
		if name == "READ_ONLY" {
			continue
		}
		if !gatesATool[name] {
			continue
		}
		env = append(env, "GDRIVE_"+name+"=true")
	}
	sort.Strings(env)
	return env, nil
}

// gatesATool names the boolean settings that decide whether a tool is
// registered. A setting that only changes behaviour is not here: setting
// GDRIVE_SHARING or GDRIVE_LOCAL_DIR to "true" would be nonsense, and a
// dump has to stay a dump.
var gatesATool = map[string]bool{
	"LABELS":             true,
	"ACTIVITY":           true,
	"ENABLE_DESTRUCTIVE": true,
}
