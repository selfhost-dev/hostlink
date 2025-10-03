package commands

import (
	"context"
	"fmt"
	"os"

	"hostlink/cmd/hlctl/client"
	"hostlink/cmd/hlctl/config"
	"hostlink/cmd/hlctl/output"

	"github.com/urfave/cli/v3"
)

// TaskCommand returns the task command with subcommands
func TaskCommand() *cli.Command {
	return &cli.Command{
		Name:  "task",
		Usage: "Manage tasks",
		Commands: []*cli.Command{
			createTaskCommand(),
			listTaskCommand(),
			getTaskCommand(),
		},
	}
}

// createTaskCommand returns the create subcommand
func createTaskCommand() *cli.Command {
	return &cli.Command{
		Name:  "create",
		Usage: "Create a new task",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "command",
				Usage: "Command to execute (inline)",
			},
			&cli.StringFlag{
				Name:  "file",
				Usage: "Path to script file to execute",
			},
			&cli.StringSliceFlag{
				Name:  "tag",
				Usage: "Target agents by tag (repeatable, format: key=value)",
			},
			&cli.IntFlag{
				Name:  "priority",
				Usage: "Task priority",
				Value: 1,
			},
		},
		Action: createTaskAction,
	}
}

// createTaskAction handles the create task command
func createTaskAction(ctx context.Context, c *cli.Command) error {
	if err := validateCreateFlags(c); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	serverURL := cfg.GetServerURL()
	if c.IsSet("server") {
		serverURL = c.String("server")
	}

	httpClient := client.NewHTTPClient(serverURL)

	command := c.String("command")
	if c.IsSet("file") {
		var err error
		command, err = readScriptFile(c.String("file"))
		if err != nil {
			return fmt.Errorf("failed to read script file: %w", err)
		}
	}

	priority := c.Int("priority")

	var agentIDs []string
	if c.IsSet("tag") {
		tags := c.StringSlice("tag")
		agents, err := httpClient.ListAgents(tags)
		if err != nil {
			return fmt.Errorf("failed to list agents: %w", err)
		}

		for _, agent := range agents {
			agentIDs = append(agentIDs, agent.ID)
		}
	}

	req := &client.CreateTaskRequest{
		Command:  command,
		Priority: priority,
		AgentIDs: agentIDs,
	}

	resp, err := httpClient.CreateTask(req)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	formatter := output.NewJSONFormatter()
	jsonOutput, err := formatter.Format(resp)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	fmt.Println(jsonOutput)
	return nil
}

// validateCreateFlags validates the create command flags
func validateCreateFlags(c *cli.Command) error {
	hasCommand := c.IsSet("command")
	hasFile := c.IsSet("file")

	if hasCommand && hasFile {
		return fmt.Errorf("cannot use both --command and --file flags")
	}

	if !hasCommand && !hasFile {
		return fmt.Errorf("must provide either --command or --file flag")
	}

	return nil
}

// readScriptFile reads and returns the contents of a script file
func readScriptFile(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// listTaskCommand returns the list subcommand
func listTaskCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List tasks",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "status",
				Usage: "Filter by status",
			},
			&cli.StringFlag{
				Name:  "agent",
				Usage: "Filter by agent ID",
			},
		},
		Action: listTaskAction,
	}
}

// listTaskAction handles the list task command
func listTaskAction(ctx context.Context, c *cli.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	serverURL := cfg.GetServerURL()
	if c.IsSet("server") {
		serverURL = c.String("server")
	}

	httpClient := client.NewHTTPClient(serverURL)

	filters := &client.ListTasksRequest{}
	if c.IsSet("status") {
		filters.Status = c.String("status")
	}
	if c.IsSet("agent") {
		filters.AgentID = c.String("agent")
	}

	tasks, err := httpClient.ListTasks(filters)
	if err != nil {
		return fmt.Errorf("failed to list tasks: %w", err)
	}

	formatter := output.NewJSONFormatter()
	jsonOutput, err := formatter.Format(tasks)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	fmt.Println(jsonOutput)
	return nil
}

// getTaskCommand returns the get subcommand
func getTaskCommand() *cli.Command {
	return &cli.Command{
		Name:      "get",
		Usage:     "Get task details",
		ArgsUsage: "<task-id>",
		Action:    getTaskAction,
	}
}

// getTaskAction handles the get task command
func getTaskAction(ctx context.Context, c *cli.Command) error {
	if c.Args().Len() != 1 {
		return fmt.Errorf("task ID is required")
	}

	taskID := c.Args().Get(0)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	serverURL := cfg.GetServerURL()
	if c.IsSet("server") {
		serverURL = c.String("server")
	}

	httpClient := client.NewHTTPClient(serverURL)

	task, err := httpClient.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}

	formatter := output.NewJSONFormatter()
	jsonOutput, err := formatter.Format(task)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}

	fmt.Println(jsonOutput)
	return nil
}
