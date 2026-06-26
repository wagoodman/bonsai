// Package mcp exposes bonsai's binary-size and go-version analysis as a Model Context Protocol
// server, so an AI agent operating inside a codebase can use bonsai as a yardstick — finding
// high-value prune candidates, locating the exact edit sites, and measuring the result — rather
// than searching and guessing in the dark. It is a thin adapter over internal/bonsai: every tool
// builds (or reuses a cached) *bonsai.Resolved and serializes a focused report.
package mcp

import "github.com/wagoodman/bonsai/internal/bonsai"

// Input is the shared input for the whole-binary tools: how to build (or load) the target and
// which modules count as 1st-class / locked. It maps directly onto bonsai.Config.
type Input struct {
	Dir        string   `json:"dir,omitempty" jsonschema:"module directory to build and analyze (default: current directory)"`
	Target     string   `json:"target,omitempty" jsonschema:"entrypoint package to build (default: the module's sole main package)"`
	Binary     string   `json:"binary,omitempty" jsonschema:"analyze a prebuilt binary instead of building from source"`
	Controlled []string `json:"controlled,omitempty" jsonschema:"1st-class module patterns whose imports are cuttable, beyond the main module (exact, glob, or path/...)"`
	Lock       []string `json:"lock,omitempty" jsonschema:"module patterns to lock (never proposed for pruning)"`
	Unlock     []string `json:"unlock,omitempty" jsonschema:"locked modules to re-open as prune candidates"`
}

// InspectInput is the input for locate_cuts: a whole-binary Input plus the single module to
// drill into.
type InspectInput struct {
	Input  `json:",inline"`
	Module string `json:"module" jsonschema:"the dependency module path to inspect (e.g. github.com/google/go-containerregistry)"`
}

// config maps the tool input onto the engine's Config.
func (in Input) config() bonsai.Config {
	return bonsai.Config{
		Dir:        in.Dir,
		Target:     in.Target,
		Binary:     in.Binary,
		Controlled: in.Controlled,
		Locked:     in.Lock,
		Unlock:     in.Unlock,
	}
}
