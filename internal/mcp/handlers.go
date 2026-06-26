package mcp

import (
	"context"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

// resolve maps the tool input onto an engine config (folding in .bonsai.yaml) and runs fn against
// the resolved (and cached) target. Centralizing this keeps every tool honoring the config file.
func (s *Server) resolve(in Input, fn func(*bonsai.Resolved) error) error {
	cfg, err := in.config()
	if err != nil {
		return err
	}
	return s.cache.with(cfg, fn)
}

// anatomy reports the binary's size shape (SizeReport), unchanged from the engine.
func (s *Server) anatomy(_ context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, bonsai.SizeReport, error) {
	var out bonsai.SizeReport
	err := s.resolve(in, func(r *bonsai.Resolved) error {
		out = r.Size()
		return nil
	})
	return nil, out, err
}

// sizeTargets ranks prune candidates by bytes freed, annotating each with cost signals and a
// coarse effort verdict, and returns the greedy prune plan.
func (s *Server) sizeTargets(_ context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, SizeTargetsOutput, error) {
	var out SizeTargetsOutput
	err := s.resolve(in, func(r *bonsai.Resolved) error {
		rep := r.Prune()
		out = SizeTargetsOutput{
			AccountedSize: rep.AccountedSize,
			MainModule:    rep.MainModule,
			Plan:          rep.Plan,
		}
		for _, m := range rep.Modules {
			if m.Prune == nil {
				continue // not a prune candidate
			}
			c := Candidate{
				Module:         m.Module,
				Class:          m.Class,
				FreedBytes:     m.Prune.FreedBytes,
				PotentialBytes: m.Prune.PotentialBytes,
				SharedWith:     m.Prune.SharedWith,
			}
			if m.Coupling != nil {
				c.ImportingPackages = m.Coupling.ImportingPackages
				c.ImportSites = m.Coupling.ImportSites
			}
			c.Verdict = sizeVerdict(c)
			out.Candidates = append(out.Candidates, c)
		}
		sort.Slice(out.Candidates, func(i, j int) bool {
			if out.Candidates[i].FreedBytes != out.Candidates[j].FreedBytes {
				return out.Candidates[i].FreedBytes > out.Candidates[j].FreedBytes
			}
			return out.Candidates[i].Module < out.Candidates[j].Module
		})
		return nil
	})
	return nil, out, err
}

// goFloor reports the dep-imposed go-version floor with the actionable verdict.
func (s *Server) goFloor(_ context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, GoFloorOutput, error) {
	var out GoFloorOutput
	err := s.resolve(in, func(r *bonsai.Resolved) error {
		f := r.GoFloor()
		out = GoFloorOutput{
			Version:     f.Version,
			OwnedMax:    f.OwnedMax,
			Critical:    f.Critical,
			NextVersion: f.NextVersion,
			Action:      goAction(f),
		}
		return nil
	})
	return nil, out, err
}

// locateCuts drills into one module: the edit sites, the rewrite-scope map, the drag-out, and
// the go-version floor delta (InspectReport), unchanged from the engine.
func (s *Server) locateCuts(_ context.Context, _ *mcp.CallToolRequest, in InspectInput) (*mcp.CallToolResult, bonsai.InspectReport, error) {
	var out bonsai.InspectReport
	err := s.resolve(in.Input, func(r *bonsai.Resolved) error {
		rep, ierr := r.Inspect(in.Module)
		if ierr != nil {
			return ierr
		}
		out = rep
		return nil
	})
	return nil, out, err
}

// measure returns just the binary size and go-version floor — the cheap before/after numbers for
// the edit loop.
func (s *Server) measure(_ context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, MeasureOutput, error) {
	var out MeasureOutput
	err := s.resolve(in, func(r *bonsai.Resolved) error {
		size := r.Size()
		floor := r.GoFloor()
		out = MeasureOutput{
			BinarySize:    size.BinarySize,
			AccountedSize: size.AccountedSize,
			GoVersion:     floor.Version,
			OwnedMax:      floor.OwnedMax,
		}
		return nil
	})
	return nil, out, err
}
