package main

import (
	"context"
	"fmt"
	"os"

	"hostlink/cmd/hlctl/commands"
)

func main() {
	app := commands.NewApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
