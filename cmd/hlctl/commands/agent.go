package commands

import (
	"context"
	"fmt"

	"hostlink/cmd/hlctl/client"
	"hostlink/cmd/hlctl/config"
	"hostlink/cmd/hlctl/output"

	"github.com/urfave/cli/v3"
)

func AgentCommand() *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "Manage agents",
		Commands: []*cli.Command{
			listAgentCommand(),
			getAgentCommand(),
		},
	}
}

func listAgentCommand() *cli.Command {
	return &cli.Command{
		Name:   "list",
		Usage:  "List all agents",
		Action: listAgentAction,
	}
}

func listAgentAction(ctx context.Context, c *cli.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	serverURL := cfg.GetServerURL()
	if c.IsSet("server") {
		serverURL = c.String("server")
	}

	httpClient := client.NewHTTPClient(serverURL)

	agents, err := httpClient.ListAgents(nil)
	if err != nil {
		return fmt.Errorf("failed to list agents: %w", err)
	}

	formatter := output.NewJSONFormatter()
	jsonOutput, err := formatter.Format(agents)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	fmt.Println(jsonOutput)
	return nil
}

func getAgentCommand() *cli.Command {
	return &cli.Command{
		Name:      "get",
		Usage:     "Get agent details",
		ArgsUsage: "<agent-id>",
		Action:    getAgentAction,
	}
}

func getAgentAction(ctx context.Context, c *cli.Command) error {
	if c.Args().Len() != 1 {
		return fmt.Errorf("agent ID is required")
	}

	agentID := c.Args().Get(0)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	serverURL := cfg.GetServerURL()
	if c.IsSet("server") {
		serverURL = c.String("server")
	}

	httpClient := client.NewHTTPClient(serverURL)

	agent, err := httpClient.GetAgent(agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent: %w", err)
	}

	formatter := output.NewJSONFormatter()
	jsonOutput, err := formatter.Format(agent)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	fmt.Println(jsonOutput)
	return nil
}
