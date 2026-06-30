// Package mcp exposes bonsai's binary-size and go-version analysis as a Model Context Protocol
// server, so an AI agent operating inside a codebase can use bonsai as a yardstick — finding
// high-value prune candidates, locating the exact edit sites, and measuring the result — rather
// than searching and guessing in the dark. It is a thin adapter over internal/bonsai: every tool
// builds (or reuses a cached) *bonsai.Resolved and serializes a focused report.
package mcp

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/configedit"
)

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

// fileConfig is the slice of .bonsai.yaml the MCP server reads directly. The server bypasses clio,
// so the config file isn't auto-loaded; reading it here is what keeps the MCP build matching how
// the project (and every CLI subject) actually builds — the persisted analysis settings and the
// check budget.
type fileConfig struct {
	Analysis options.Build `yaml:"analysis"`
	Check    options.Check `yaml:"check"`
}

// loadFileConfig reads the project's .bonsai.yaml. A missing file yields a zero config (no error);
// a malformed file surfaces the parse error.
func loadFileConfig(dir string) (fileConfig, error) {
	var fc fileConfig
	data, err := os.ReadFile(configedit.FindConfig(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return fc, nil
		}
		return fc, err
	}
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return fc, fmt.Errorf("parsing config: %w", err)
	}
	return fc, nil
}

// load folds the agent input and the project's .bonsai.yaml analysis section into one options.Build
// and resolves it (PostLoad), exactly as the CLI does — including persisted build settings
// (tags/env/args) and any .goreleaser.yaml-derived build. Without this an agent that omits
// lock/controlled/unlock would silently ignore the curated locks, and every build would drop the
// project's real build flags. Agent-provided patterns are unioned with the file's; an explicit
// target/binary from the agent overrides the file. The check budget is returned alongside for the
// check tool.
func (in Input) load() (options.Build, options.Check, error) {
	fc, err := loadFileConfig(in.Dir)
	if err != nil {
		return options.Build{}, options.Check{}, err
	}
	b := fc.Analysis
	b.Dir = in.Dir
	if in.Target != "" {
		b.Target = in.Target
	}
	if in.Binary != "" {
		b.Binary = in.Binary
	}
	b.Controlled = mergeUnique(b.Controlled, in.Controlled)
	b.Lock = mergeUnique(b.Lock, in.Lock)
	b.Unlock = mergeUnique(b.Unlock, in.Unlock)
	// PostLoad resolves goreleaser (if present) and folds the host build into BuildSettings, the
	// same single choke point the CLI uses.
	if err := b.PostLoad(); err != nil {
		return options.Build{}, options.Check{}, err
	}
	return b, fc.Check, nil
}

// config maps the tool input onto the engine's Config, folding in the project's .bonsai.yaml.
func (in Input) config() (bonsai.Config, error) {
	b, _, err := in.load()
	if err != nil {
		return bonsai.Config{}, err
	}
	return b.Config(), nil
}

// mergeUnique unions two pattern lists into a sorted, de-duplicated slice (nil if empty), so the
// merge of agent input and config file is order-independent and keeps the resolve cache key stable.
func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
