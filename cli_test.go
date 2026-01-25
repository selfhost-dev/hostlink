package main

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"
	"hostlink/version"
)

func TestCLIApp_VersionFlag(t *testing.T) {
	app := newApp()

	if app.Version != version.Version {
		t.Errorf("expected version %q, got %q", version.Version, app.Version)
	}
}

func TestCLIApp_HasVersionCommand(t *testing.T) {
	app := newApp()

	var found bool
	for _, cmd := range app.Commands {
		if cmd.Name == "version" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'version' subcommand to exist")
	}
}

func TestCLIApp_HasUpgradeCommand(t *testing.T) {
	app := newApp()

	var found bool
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'upgrade' subcommand to exist")
	}
}

func TestCLIApp_UpgradeHasInstallPathFlag(t *testing.T) {
	app := newApp()

	var upgradeCmd *cli.Command
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			upgradeCmd = cmd
			break
		}
	}
	if upgradeCmd == nil {
		t.Fatal("upgrade command not found")
	}

	var found bool
	for _, f := range upgradeCmd.Flags {
		if hasName(f, "install-path") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'upgrade' to have --install-path flag")
	}
}

func TestCLIApp_UpgradeInstallPathDefaultValue(t *testing.T) {
	app := newApp()

	var upgradeCmd *cli.Command
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			upgradeCmd = cmd
			break
		}
	}
	if upgradeCmd == nil {
		t.Fatal("upgrade command not found")
	}

	for _, f := range upgradeCmd.Flags {
		if hasName(f, "install-path") {
			sf, ok := f.(*cli.StringFlag)
			if !ok {
				t.Fatal("install-path is not a StringFlag")
			}
			if sf.Value != "/usr/bin/hostlink" {
				t.Errorf("expected default '/usr/bin/hostlink', got %q", sf.Value)
			}
			return
		}
	}
	t.Error("install-path flag not found")
}

func TestCLIApp_UpgradeActionIsWired(t *testing.T) {
	app := newApp()

	var upgradeCmd *cli.Command
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			upgradeCmd = cmd
			break
		}
	}
	if upgradeCmd == nil {
		t.Fatal("upgrade command not found")
	}

	if upgradeCmd.Action == nil {
		t.Error("upgrade action should be wired (not nil)")
	}
}

func TestCLIApp_UpgradeHasDryRunFlag(t *testing.T) {
	app := newApp()

	var upgradeCmd *cli.Command
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			upgradeCmd = cmd
			break
		}
	}
	if upgradeCmd == nil {
		t.Fatal("upgrade command not found")
	}

	var found bool
	for _, f := range upgradeCmd.Flags {
		if hasName(f, "dry-run") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'upgrade' to have --dry-run flag")
	}
}

func TestCLIApp_UpgradeHasUpdateIDFlag(t *testing.T) {
	app := newApp()

	var upgradeCmd *cli.Command
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			upgradeCmd = cmd
			break
		}
	}
	if upgradeCmd == nil {
		t.Fatal("upgrade command not found")
	}

	var found bool
	for _, f := range upgradeCmd.Flags {
		if hasName(f, "update-id") {
			sf, ok := f.(*cli.StringFlag)
			if !ok {
				t.Fatal("update-id is not a StringFlag")
			}
			if !sf.Hidden {
				t.Error("update-id flag should be hidden")
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'upgrade' to have --update-id flag")
	}
}

func TestCLIApp_UpgradeHasSourceVersionFlag(t *testing.T) {
	app := newApp()

	var upgradeCmd *cli.Command
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			upgradeCmd = cmd
			break
		}
	}
	if upgradeCmd == nil {
		t.Fatal("upgrade command not found")
	}

	var found bool
	for _, f := range upgradeCmd.Flags {
		if hasName(f, "source-version") {
			sf, ok := f.(*cli.StringFlag)
			if !ok {
				t.Fatal("source-version is not a StringFlag")
			}
			if !sf.Hidden {
				t.Error("source-version flag should be hidden")
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'upgrade' to have --source-version flag")
	}
}

func TestCLIApp_UpgradeHasBaseDirFlag(t *testing.T) {
	app := newApp()

	var upgradeCmd *cli.Command
	for _, cmd := range app.Commands {
		if cmd.Name == "upgrade" {
			upgradeCmd = cmd
			break
		}
	}
	if upgradeCmd == nil {
		t.Fatal("upgrade command not found")
	}

	var found bool
	for _, f := range upgradeCmd.Flags {
		if hasName(f, "base-dir") {
			sf, ok := f.(*cli.StringFlag)
			if !ok {
				t.Fatal("base-dir is not a StringFlag")
			}
			if !sf.Hidden {
				t.Error("base-dir flag should be hidden")
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'upgrade' to have --base-dir flag")
	}
}

func TestCLIApp_DefaultActionExists(t *testing.T) {
	app := newApp()

	// The default action (no subcommand) should be set
	if app.Action == nil {
		t.Error("expected default action to be set (starts Echo server)")
	}
}

func TestCLIApp_Name(t *testing.T) {
	app := newApp()

	if app.Name != "hostlink" {
		t.Errorf("expected app name 'hostlink', got %q", app.Name)
	}
}

func hasName(f cli.Flag, name string) bool {
	for _, n := range f.Names() {
		if n == name {
			return true
		}
	}
	return false
}

// Suppress unused import
var _ = context.Background
