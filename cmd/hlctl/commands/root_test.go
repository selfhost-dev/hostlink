package commands

import (
	"bytes"
	"context"
	"testing"

	"hostlink/version"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewApp(t *testing.T) {
	app := NewApp()
	require.NotNil(t, app)
	assert.Equal(t, "hlctl", app.Name)
	assert.NotEmpty(t, app.Usage)
}

func TestAppVersion(t *testing.T) {
	app := NewApp()
	require.NotNil(t, app)
	assert.Equal(t, version.Version, app.Version)
}

func TestAppHasHelpFlag(t *testing.T) {
	app := NewApp()
	require.NotNil(t, app)

	var buf bytes.Buffer
	app.Writer = &buf

	err := app.Run(context.Background(), []string{"hlctl", "--help"})
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "hlctl", "Help should contain app name")
	assert.Contains(t, output, "Hostlink CLI", "Help should contain usage description")
	assert.Contains(t, output, "USAGE", "Help should contain USAGE section")
}
