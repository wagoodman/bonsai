# Bonsai

*Make smaller dependency trees for your Go projects.*

`bonsai` builds your binary and finds the dependencies driving its size and your minimum Go version, including the ones pulled in transitively that you never imported directly, then works out how much each one would actually save if you pruned it. Some of them you'll genuinely need, and that call stays yours; bonsai just gives you the numbers to make it with. There's also an MCP server, so an AI agent working in your codebase can use it to find and make cuts instead of guessing.

The thing it's built around: "this dep is 8 MB, so dropping it saves 8 MB" is almost never true. Most of that weight is shared with other dependencies that aren't going anywhere. What matters isn't how big a dependency is, it's how much *only it* is keeping alive. That's the same "retained size" a memory profiler shows you, applied to your dependency graph.

Go already gives you most of the raw data here. `go mod graph` dumps every edge, and `go mod why` tells you why one module is in the build. But on a real project that's a wall of per-edge, per-module output, and it's all forest-for-the-trees: you can answer "why is X here?" one module at a time, but not "which deps actually matter for my size, and what do I get back if I cut them?" bonsai is the forest view. It pulls the same reachability into one ranked picture and puts real byte weights on it.

*Why is it called `bonsai`? Named for the practice of keeping a tree small and healthy through deliberate pruning.*


## Try it

```sh
bonsai prune .          # rank what's worth pruning in your go module
```

It compiles your target, which is how it gets ground truth instead of guessing from `go.mod`. Already have a built binary? Add `--binary ./mything` to skip the build.

There are a handful of subcommands, each answering one question:

| Command | The question |
|---|---|
| `bonsai .` | What's in my binary, and which module owns each byte? |
| `bonsai prune .` | Which dependencies are worth cutting, ranked, and in what order? |
| `bonsai go-version .` | How low can my `go` directive go, and which deps pin it? |
| `bonsai inspect MODULE .` | I'm cutting module X, so which files do I edit, and what happens? |
| `bonsai check .` | Is the committed budget still met? (a CI gate; non-zero exit on violation) |

## The one knob worth setting

**`--controlled` is the flag that makes bonsai useful, and if you don't set it you'll get shallow results.**

By default bonsai assumes the only code you can edit is the module you're scanning, so the only refactors it considers are cutting your direct dependencies. *But "code you can edit" is usually bigger than one module*: your org's libraries, a fork you maintain, anything you can send a PR to. Mark one of those as controlled and bonsai can suggest cutting an import inside *that* library, a dependency of a dependency, below anything your `go.mod` mentions. That's usually where the real savings hide, and they're invisible until you tell bonsai which code is yours to change. So widen this as far as it honestly goes.

```sh
bonsai prune . --controlled "github.com/yourorg/..."
```

A few more flags, briefly:

- `--lock <pattern>`: lock things you'll never drop so they stop showing up as suggestions.
- `--unlock <module>`: the opposite, treating something you own as fair game to drop wholesale.
- `--blame`: split each dependency's fair share of the shared weight, so the numbers add up to the real total instead of crediting shared deps to nobody (the Shapley value).

### How bonsai sorts your dependencies

Every dependency lands in one of four buckets, and the bucket decides whether bonsai will ever suggest cutting it:

- **1st-class**: code you control, meaning your main module plus anything `--controlled` matches. bonsai never prunes these; it looks for imports to cut out of them.
- **2nd-class**: a dependency your 1st-class code imports directly. These are the actual prune candidates, the ones you import yourself, so you can change your code to drop them.
- **3rd-class**: a dependency reached only *through* other dependencies. You can't drop it directly; it leaves only when whatever pulls it in leaves. (Most of your graph.)
- **locked**: off-limits, never suggested. Everything 1st-class is locked by default; `--lock` locks more, `--unlock` explicitly re-opens modules for consideration.

Widening `--controlled` promotes 3rd-class deps into 2nd-class candidates.

## Keep the cut cut

Pruning a dependency once doesn't keep it gone: a `go get` re-adds it, a transitive bump quietly grows the binary, a new dep raises your `go` floor. `bonsai check` turns the analysis into a CI gate. It reads a committed budget from `.bonsai.yaml`, runs the same build-and-resolve as the other commands, and exits non-zero when the budget is violated.

```yaml
check:
  max-binary-size: 25MB          # gates the accounted (~ stripped / release) size; --binary gates the on-disk size instead
  max-go-version: "1.23"         # fail if the dep-imposed go floor climbs above this
  deny:                          # modules that must never reappear in the build
    - github.com/aws/aws-sdk-go
    - cloud.google.com/go/...
  max-module-size:               # optional per-module size caps (pattern -> size)
    github.com/klauspost/compress: 2MB
  action: fail                   # what a violation does: fail (non-zero exit) | warn (print only)
```

```sh
bonsai check .                   # exit 0 = pass, 2 = budget violated, 1 = tool/config error
```

Exit code 2 means "the gate failed" and 1 means "the tool broke", so CI can tell them apart. `deny` and `max-module-size` take the same patterns as `--lock`/`--controlled` (`github.com/org/...`, globs). `--output json` emits the machine form. An absent `check:` block exits 0 with a note. Set `action: warn` to print violations without failing the build.

## Four ways to look at it

The same analysis comes out in whatever form fits who's reading it:

- **Tables** (`--output table`, the default): for you, at the terminal. The quick read.
- **JSON** (`--output json`): for machines, like scripts, CI gates, your own tooling.
- **The TUI** (`bonsai` with no subcommand): for grokking. When you want to try prunes and watch them interact.
- **MCP** (`bonsai mcp`): for an AI agent editing your codebase.

### Tables, the quick read

`bonsai prune` ranks your prune candidates and lays out a plan. The headline is the cut most likely to be worth it:

```
best single win: prune github.com/modelcontextprotocol/go-sdk → 1.1 MB now,
                 65.6% of the 1.7 MB freeable in its subtree
                 (587.5 kB shared, co-prune bubbly, clio, bubbles +2 more to free it)
```

Prunes interact, since shared weight only frees once the last thing holding it is gone, so the plan orders the cuts and shows, for each one, whether the weight is the dep's own code or the stuff it drags out behind it:

```
1.  +1.1 MB  (cumulative 1.1 MB)  github.com/modelcontextprotocol/go-sdk
     own code    277.8 kB  24.8%
     drags out   841.9 kB  75.2%
        249.5 kB  math/big (std)
        212.1 kB  github.com/segmentio/encoding
        143.5 kB  github.com/google/jsonschema-go
        ...
```

So the go-sdk module is a quarter its own code, and the rest is the cluster it pulls in. Worth knowing before you start removing imports.

### The TUI

```sh
bonsai .
```

Everything starts checked (in your build). Uncheck a dependency and the header reprojects the size right away, while the side panes show what it drags out, what survives because something else still needs it, and why it's in the build. The `M`/`1`/`2`/`3`/`L` column is the four classes above; reclassify on the fly and watch the candidate set move. Nothing is applied (enter just prints the prune set), but lock and class edits save to `.bonsai.yaml`, the same file every command reads. Press `?` for the legend.

### MCP

```sh
bonsai mcp        # a Model Context Protocol server over stdio
```

Why a server instead of just shelling out to the CLI? Because the CLI rebuilds your target on every run, and an agent doesn't ask once: it orients, locates cuts, edits, re-measures, repeats. The server builds once and keeps it warm, rebuilding only when you actually change the source, so that loop stays cheap. Point an MCP client at it and the agent uses bonsai as a yardstick, editing with intent instead of guessing. It honors the lock and class lists in your `.bonsai.yaml` just like every other command, so the agent respects the locks you curated even when it doesn't pass them itself. Five tools, each one focused analysis:

| Tool | The agent's question |
|---|---|
| `bonsai_size_targets` | Where are the biggest size wins, ranked, and in what order? |
| `bonsai_go_floor` | How low can my `go` directive go, and which deps pin it? |
| `bonsai_locate_cuts` | I'm cutting module X, so which files/lines do I edit, and what happens? |
| `bonsai_anatomy` | What's the binary's size shape now? |
| `bonsai_measure` | Did my edit shrink it / lower the floor? (a cheap re-check) |

`bonsai_locate_cuts` returns the concrete first-party import sites (`file:line`) to edit and the per-entry-package bytes behind them, so a partial rewrite is scoped to what's actually worth it. Then ask the agent to "make this binary smaller by replacing high-value dependencies" or "lower our minimum Go version."
