package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadScriptFile_FileExists(t *testing.T) {
	filePath := createTempScriptFile(t, "#!/bin/bash\necho 'hello'")
	defer os.Remove(filePath)

	content, err := readScriptFile(filePath)

	require.NoError(t, err)
	assert.Equal(t, "#!/bin/bash\necho 'hello'", content)
}

func TestReadScriptFile_FileDoesNotExist(t *testing.T) {
	_, err := readScriptFile("/nonexistent/script.sh")

	require.Error(t, err)
}

func TestReadScriptFile_EmptyFile(t *testing.T) {
	filePath := createTempScriptFile(t, "")
	defer os.Remove(filePath)

	content, err := readScriptFile(filePath)

	require.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestCreateTaskAction_BuildsRequestWithCommand(t *testing.T) {
	t.Skip("TODO: Implement after createTaskAction is implemented")
}

func TestCreateTaskAction_BuildsRequestWithFile(t *testing.T) {
	t.Skip("TODO: Implement after createTaskAction is implemented")
}

func TestCreateTaskAction_ResolvesAgentIDsFromSingleTag(t *testing.T) {
	t.Skip("TODO: Implement after createTaskAction is implemented")
}

func TestCreateTaskAction_ResolvesAgentIDsFromMultipleTags(t *testing.T) {
	t.Skip("TODO: Implement after createTaskAction is implemented")
}

func TestCreateTaskAction_BuildsRequestWithPriority(t *testing.T) {
	t.Skip("TODO: Implement after createTaskAction is implemented")
}

func TestCreateTaskAction_BuildsRequestWithDefaultPriority(t *testing.T) {
	t.Skip("TODO: Implement after createTaskAction is implemented")
}

func createTempScriptFile(t *testing.T, content string) string {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "script.sh")

	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	return filePath
}
