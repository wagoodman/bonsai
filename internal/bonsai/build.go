package bonsai

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// buildArtifacts is the output of building the analysis target from source: the compiled
// (unstripped) binary plus the linker's symbol-dependency dump captured from `-dumpdep`.
type buildArtifacts struct {
	Binary  string // path to the compiled unstripped binary
	Dumpdep string // path to the captured `-ldflags=-dumpdep` output (post-DCE reachability)
}

// buildForAnalysis compiles target in dir into a temporary unstripped binary and captures
// the linker's `-dumpdep` symbol-reachability graph alongside it. Building ourselves means
// we always have matching source + binary, never need to locate a checkout or rebuild a
// stripped artifact, and get the exact post-dead-code-elimination reference graph the
// linker actually used. p selects the build cell (GOOS/GOARCH/tags); b carries persisted build
// defaults (env, extra tags, freeform args). Zero values are the host toolchain. Returns the
// artifacts and a cleanup func that removes both temps.
func buildForAnalysis(dir, target string, p Platform, b BuildSettings) (buildArtifacts, func(), error) {
	binF, err := os.CreateTemp("", "bonsai-bin-*")
	if err != nil {
		return buildArtifacts{}, func() {}, err
	}
	binF.Close()
	ddF, err := os.CreateTemp("", "bonsai-dumpdep-*")
	if err != nil {
		os.Remove(binF.Name())
		return buildArtifacts{}, func() {}, err
	}
	cleanup := func() {
		os.Remove(binF.Name())
		os.Remove(ddF.Name())
	}

	// -dumpdep writes the symbol dependency graph to stderr during the link step; capture
	// it to a file. On a build failure the link step never runs, so stderr holds only the
	// compiler error, which we surface for diagnostics.
	// user args first, then bonsai's own -o/-ldflags=-dumpdep/-tags so they can't be clobbered:
	// the analysis build must stay unstripped and emit the dumpdep graph regardless of what the
	// user persisted. as a result -tags/-o/-ldflags inside b.Args are overridden — put build tags
	// in the tags/build.tags field, not args.
	args := []string{"build"}
	args = append(args, splitArgs(b.Args)...)
	args = append(args, "-o", binF.Name(), "-ldflags=-dumpdep")
	if tags := tagsArg(effectiveTags(b, p)); tags != "" {
		args = append(args, tags)
	}
	args = append(args, target)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = platformEnv(p, b)
	cmd.Stderr = ddF
	runErr := cmd.Run()
	ddF.Close()
	if runErr != nil {
		msg := tailFile(ddF.Name(), 4096)
		cleanup()
		return buildArtifacts{}, func() {}, fmt.Errorf("building %s in %s: %w\n%s", target, dir, runErr, msg)
	}

	return buildArtifacts{Binary: binF.Name(), Dumpdep: ddF.Name()}, cleanup, nil
}

// tailFile returns up to the last limit bytes of the file at path (build diagnostics live at
// the end). Best-effort: returns "" if the file can't be read.
func tailFile(path string, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	return strings.TrimSpace(string(data))
}
