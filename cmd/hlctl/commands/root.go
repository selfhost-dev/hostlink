package commands

import (
	"hostlink/version"

	"github.com/urfave/cli/v3"
)

// NewApp creates the root CLI application
func NewApp() *cli.Command {
	return &cli.Command{
		Name:    "hlctl",
		Usage:   "Hostlink CLI - manage tasks and agents",
		Version: version.Version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "server",
				Usage: "Hostlink server URL",
			},
		},
		Commands: []*cli.Command{
			TaskCommand(),
		},
	}
}
