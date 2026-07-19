# Bonsai

*Find what's driving the size of your Go binary.*

<img width="956" height="739" alt="bonsai TUI" src="https://github.com/user-attachments/assets/13915b6a-ef67-4e81-b415-6e08bbf12556" />

`bonsai` compiles your binary and finds the dependencies driving its size and your minimum Go version, including the ones pulled in transitively that you never imported directly, then works out how much each one would actually save if you pruned it. Some of them you'll genuinely need, and that call stays yours; bonsai just gives you the numbers to make it with. There's also an MCP server, so an AI agent working in your codebase can use it to find and make cuts instead of guessing.

*Why is it called `bonsai`? Named for the practice of keeping a tree small and healthy through deliberate pruning.*

> [!NOTE]
> **Full documentation is at [bonsai.dev docs](https://wagoodman.github.io/bonsai/)** — getting started, every subcommand and the TUI, the MCP server, the `.bonsai.yaml` config and CI gate, and the [methodology deep-dive](https://wagoodman.github.io/bonsai/docs/methodology/). This README is just the short pitch.

## Install

```sh
curl -sSfL https://raw.githubusercontent.com/wagoodman/bonsai/main/install.sh | sh
```

That picks a sensible spot on its own: an existing writable dir on your `PATH` (like `~/.local/bin`) if you have one, otherwise `/usr/local/bin`, prompting for `sudo` only when it has to. Pass `-b DIR` for a specific location, pin a version by appending a tag (`... | sh -s -- v0.1.0`), or grab a prebuilt archive from the [releases page](https://github.com/wagoodman/bonsai/releases).

## The idea

"This dep is 8 MB, so dropping it saves 8 MB" is almost never true. Most of that weight is shared with other dependencies that aren't going anywhere. What matters isn't how big a dependency is, it's how much *only it* is keeping alive. That's the same "retained size" a memory profiler shows you, applied to your dependency graph.

Go already gives you most of the raw data here. `go mod graph` dumps every edge, and `go mod why` tells you why one module is in the build. But on a real project that's a wall of per-edge, per-module output, and it's all forest-for-the-trees: you can answer "why is X here?" one module at a time, but not "which deps actually matter for my size, and what do I get back if I cut them?" bonsai is the forest view. It pulls the same reachability into one ranked picture and puts real byte weights on it.

Which cuts are even possible comes down to how bonsai classifies each module:

- **1st-class**: code you control, meaning your main module plus anything `--controlled` matches. Never pruned; bonsai looks for imports to cut out of it.
- **2nd-class**: a dependency your 1st-class code imports directly. The real prune candidates.
- **3rd-class**: reached only *through* other dependencies. Most of your graph, and where most of the weight hides. It leaves only when whatever pulls it in leaves.
- **locked**: off-limits, never suggested.

Widening `--controlled` promotes 3rd-class deps into 2nd-class candidates. That's the one lever, and it's how you reach the weight buried below your `go.mod`.
