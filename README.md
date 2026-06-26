# Bonsai

*Make smaller dependency trees for your Go projects.*

You pull in a dependency for one helper, and it quietly brings a dozen of its own along for the ride. Over time your `go.mod` grows, your binary grows, and the minimum Go version creeps up, and it's hard to see how each dependency affects these things.

`bonsai` builds your binary and finds the dependencies driving its size and your minimum Go version, then runs the *what-if*: how much each one would really save if you pruned it. Some of them you'll genuinely need, and that call stays yours; `bonsai` just gives you honest numbers to make it with. Point an AI agent at it over MCP and it'll guide the agent through the pruning instead of leaving it to guess.

The idea it's built around: **"this dep is 8 MB, so dropping it saves 8 MB" is almost never true.** Most of that weight is shared with other dependencies that aren't going anywhere. What matters isn't how big a dependency is. It's how much *only it* is keeping alive (the same "retained size" a memory profiler shows you, applied to your dependency graph).

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

## The one knob worth knowing

By default bonsai assumes the only code you can edit is your main module, so it only suggests pruning your direct dependencies. But you probably own more than that: your org's libraries, a fork you maintain. Tell it:

```sh
bonsai prune . --controlled "github.com/yourorg/..."
```

Now it'll also consider cutting a dependency out of *those*, e.g. "your stereoscope fork could stop importing go-containerregistry." That's usually where the real savings are, a layer or two down from your `go.mod`.

A few more flags, briefly:

- `--lock <pattern>`: lock things you'll never drop so they stop showing up as suggestions.
- `--unlock <module>`: the opposite, treating something you own as fair game to drop wholesale.
- `--blame`: split each dependency's fair share of the shared weight, so the numbers add up to the real total instead of crediting shared deps to nobody (the Shapley value, from cooperative game theory).

### How bonsai sorts your dependencies

That knob is really about *classifying* your graph. Every dependency lands in one of
four buckets, and the bucket decides whether bonsai will ever suggest cutting it:

- **1st-class**: code you control, meaning your main module plus anything `--controlled` matches. bonsai never prunes these; it looks for imports to cut out of* them.
- **2nd-class**: a dependency your 1st-class code imports directly. These are the actual prune candidates, the imports you could realistically stop writing.
- **3rd-class**: a dependency reached only *through* other dependencies. You can't drop it directly; it leaves only when whatever pulls it in leaves. (Most of your graph.)
- **locked**: off-limits, never suggested. Everything 1st-class is locked by default; `--lock` locks more, `--unlock` re-opens one of your own modules.

Widening `--controlled` promotes a whole layer of 3rd-class deps into 2nd-class candidates, which is exactly why the real savings tend to hide a level or two down.

## Four ways to look at it

The same analysis comes out in whatever form fits who's reading it:

- **Tables** (`--output table`, the default): for you, at the terminal. The quick read.
- **JSON** (`--output json`): for machines, like scripts, CI gates, your own tooling.
- **The `explore` TUI**: for grokking. When you want to *try* prunes and watch them interact.
- **MCP** (`bonsai mcp`): for an AI agent editing your codebase.

### Tables, the quick read

`bonsai prune` ranks your prune candidates and lays out a plan. The headline is the
cut most likely to be worth it:

```
best single win: prune github.com/modelcontextprotocol/go-sdk → 1.1 MB now,
                 65.6% of the 1.7 MB freeable in its subtree
                 (587.5 kB shared, co-prune bubbly, clio, bubbles +2 more to free it)
```

Prunes interact, since shared weight only frees once the last thing holding it is gone, so the plan orders the cuts and shows, for each one, whether the weight is the dep's *own code* or the stuff it **drags out** behind it:

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
bonsai explore .
```

Everything in your binary starts checked (included in your build). Uncheck a dependency and the header shows the new projected size right away; the side panes show what that module drags out, what survives because something else still needs it, and *why* it's in the build at all. The `M`/`1`/`2`/`3`/`L` column is the four classes from above, and you can re-classify modules on the fly and watch the candidate set move. The prune set isn't applied — `enter` just prints it — but lock/class edits you make are saved to `.bonsai.yaml` (the same list `bonsai config lock` writes, honored by every command), and your what-if selection is remembered per target between runs. Press `?` for the full legend.

### MCP

```sh
bonsai mcp        # a Model Context Protocol server over stdio
```

Point an MCP client at it and an agent working in your codebase can use bonsai as a yardstick, finding high-value cuts and editing with intent instead of guessing. Five tools, each the JSON of one focused analysis:

| Tool | The agent's question |
|---|---|
| `bonsai_size_targets` | Where are the biggest size wins, ranked, and in what order? |
| `bonsai_go_floor` | How low can my `go` directive go, and which deps pin it? |
| `bonsai_locate_cuts` | I'm cutting module X, so which files/lines do I edit, and what happens? |
| `bonsai_anatomy` | What's the binary's size shape now? |
| `bonsai_measure` | Did my edit shrink it / lower the floor? (a cheap re-check) |

`bonsai_locate_cuts` returns the concrete first-party import sites (`file:line`) to edit and the per-entry-package bytes behind them, so a partial rewrite is scoped to what's actually worth it. Builds are cached and re-run when the source changes, so the agent's edit→measure loop just works. Then ask it to "make this binary smaller by replacing high-value dependencies" or "lower our minimum Go version."

## The fine print

The estimates come from the linker's real, post-dead-code-elimination reachability, so they reflect what's in the binary, not a source-level approximation. The how and the why (the dominator trees, the garbage-collector analogy, the prior art it leans on) live in the package doc:

```sh
go doc ./internal/bonsai
```
