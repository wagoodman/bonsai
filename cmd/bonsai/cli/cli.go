package cli

import (
	"os"

	"github.com/anchore/clio"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/commands"
	"github.com/wagoodman/bonsai/cmd/bonsai/internal/ui"
	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/log"
	"github.com/wagoodman/bonsai/internal/redact"
)

func New(id clio.Identification) clio.Application {
	clioCfg := clio.NewSetupConfig(id).
		WithGlobalConfigFlag().   // add persistent -c <path> for reading an application config from
		WithGlobalLoggingFlags(). // add persistent -v and -q flags tied to the logging config
		WithConfigInRootHelp().   // --help on the root command renders the full application config in the help text
		WithUIConstructor(
			// select a UI based on the logging configuration and state of stdin (if stdin is a tty)
			func(cfg clio.Config) (*clio.UICollection, error) {
				noUI := ui.None()
				if !cfg.Log.AllowUI(os.Stdin) {
					return clio.NewUICollection(noUI), nil
				}

				return clio.NewUICollection(
					ui.New(false, cfg.Log.Quiet),
					noUI,
				), nil
			},
		).
		WithInitializers(
			func(state *clio.State) error {
				// clio is setting up and providing the bus, redact store, and logger to the application. Once loaded,
				// we can hoist them into the internal packages for global use.

				bus.Set(state.Bus)
				redact.Set(state.RedactStore)
				log.Set(state.Logger)

				return nil
			},
		)

	app := clio.New(*clioCfg)

	root := commands.Root(app)

	root.AddCommand(clio.VersionCommand(id))
	root.AddCommand(commands.Prune(app))
	root.AddCommand(commands.GoVersion(app))
	root.AddCommand(commands.Inspect(app))
	root.AddCommand(commands.Config(app))
	root.AddCommand(commands.Lock())
	root.AddCommand(commands.Explore(id))
	root.AddCommand(commands.MCP(id))

	return app
}
