package main

import (
	"context"
	"os"

	"hostlink/cmd/hlctl/commands"
)

func main() {
	app := commands.NewApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		os.Exit(1)
	}
}
