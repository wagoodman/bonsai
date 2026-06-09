# bonsai

*What's actually in your Go binary — and which dependency might be worth pruning.*

Your release binary is 40 MB. You've got a hunch it's that one heavyweight dependency you
pulled in for a single helper. So you drop it, rebuild, and... it's 39.4 MB. The other 39 MB
came along for reasons you couldn't see just from the `go.mod`.

bonsai is for that hunch. It builds your binary, looks at what the linker *actually* kept
(not what your `go.mod` claims), and tells you what pruning each dependency would really save.

Here's the idea it's built around: **"this dep is 8 MB, so dropping it saves 8 MB" is almost
never true.** Half of that 8 MB is shared with three other dependencies that aren't going
anywhere. The number you care about isn't how big a dependency is — it's how much *only it*
is keeping alive.

If that sounds familiar, it's the same question a garbage collector answers about your heap,
and the same "retained size" a memory profiler shows you. bonsai applies that idea to your
dependency graph.

## What you get

The headline is the prune most likely to be worth it:

```
best single win: prune clio → 1.6 MB now, 71.1% of the 2.2 MB freeable in its subtree
                 (639.7 kB shared — co-prune cobra, pflag to free it)
```

…and a plan that respects the fact that prunes interact — shared weight only frees once the
last thing holding it is gone. Each step shows whether a dependency's weight is *its own code*
or the stuff it **drags out** behind it:

```
1.  +1.6 MB  (cumulative 1.6 MB)  github.com/anchore/clio
     own code     53.0 kB   3.4%
     drags out     1.5 MB  96.6%
        473.0 kB  (standard library)
        313.2 kB  go.yaml.in/yaml/v3
        152.4 kB  github.com/google/pprof
        ...
```

So `clio` itself is tiny — the payoff is the dependency cluster it pulls in with it. Worth
knowing before you start removing imports.

## Try it

```sh
go run ./cmd/bonsai .          # analyze the module in the current directory
# or build it and point it somewhere
go build -o bonsai ./cmd/bonsai
./bonsai ./path/to/module
```

It compiles your target — that's how it gets ground truth instead of guesses — so point it at
something buildable. Already have a built binary? `bonsai --binary ./mything`.

Want JSON or markdown instead of the table? `--output json` / `--output markdown`.

## Explore it interactively

The table is the quick read. When you want to actually *try* prunes and watch them
interact, there's a TUI:

```sh
bonsai explore .          # takes the same flags: --controlled, --ignore, --unlock
```

Everything in your binary starts checked. Uncheck a dependency and the header shows
the new projected size right away; the side panes show what that module **drags out**
with it (and what survives, because something else still needs it), and *why* it's in
the build in the first place. You can re-classify modules on the fly — control them,
lock them, unlock them — and watch the candidate set move.

It's read-only: nothing is changed on disk, and your selection is remembered per
target between runs. **Press `?` for the full legend** — what the `M`/`1`/`2`/`3`/`L`
classes mean, the glyphs, the panes, and the keys.

## The one knob worth knowing

By default bonsai assumes the only code you can edit is your main module — so the only things
it'll suggest pruning are your top-level direct dependencies. But you probably own more than
that: your org's libraries, that fork you maintain. Tell it:

```sh
bonsai . --controlled "github.com/yourorg/..."
```

Now it'll consider pruning a dependency out of *those* too — e.g. "your stereoscope fork
could stop importing go-containerregistry." That's usually where the real savings are, a
layer or two down from your `go.mod`.

A few more, briefly:

- `--ignore <pattern>` — lock things you'll never drop (they stop showing up as suggestions).
- `--unlock <module>` — the opposite; treat something you own as fair game to drop wholesale.
- `--blame` — split each dependency's fair share of the shared weight, so the numbers add up
  to the real total instead of crediting shared deps to nobody (the Shapley value, from
  cooperative game theory).

### How bonsai sorts your dependencies

That one knob is really about *classifying* your graph, and the whole report — and the
`explore` TUI's `M`/`1`/`2`/`3`/`L` column — is organized around four buckets:

- **1st-class** — code you control: your main module, plus anything matched by
  `--controlled`. bonsai never prunes these; it looks for imports it can cut *out of* them.
- **2nd-class** — a dependency your 1st-class code imports directly. These are the actual
  prune **candidates** — the imports you could realistically stop writing.
- **3rd-class** — a dependency reached only *through* other dependencies, never from your
  own code. You can't drop it directly; it leaves only when whatever pulls it in leaves.
  (This is most of your graph.)
- **locked** — off-limits, never suggested. Everything 1st-class is locked from being
  dropped by default; `--ignore` locks more, and `--unlock` re-opens one of your own
  modules as fair game to remove wholesale.

So widening `--controlled` promotes a whole layer of 3rd-class deps into 2nd-class
candidates — which is exactly why the real savings tend to hide a level or two down.

## The fine print

bonsai is an advisor — it tells you what a prune would cost and save; you do the actual
pruning. The estimates come from the linker's real, post-dead-code-elimination reachability,
so they reflect what's in the binary, not source-level approximation.

The how and the why — the dominator trees, the garbage-collector analogy, the prior art it
leans on — live in the package doc, for when you want to go deeper:

```sh
go doc github.com/wagoodman/bonsai/bonsai
```

---

*Named for the practice of keeping a tree small and healthy through deliberate pruning.*
