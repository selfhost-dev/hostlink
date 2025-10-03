package output

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJSONFormatter(t *testing.T) {
	formatter := NewJSONFormatter()

	require.NotNil(t, formatter)
}

func TestFormat_FormatsStruct(t *testing.T) {
	formatter := NewJSONFormatter()
	data := testStruct{
		ID:     "test-123",
		Status: "active",
	}

	result, err := formatter.Format(data)

	require.NoError(t, err)
	assertValidJSON(t, result)
	assert.Contains(t, result, `"id":"test-123"`)
	assert.Contains(t, result, `"status":"active"`)
}

func TestFormat_FormatsMap(t *testing.T) {
	formatter := NewJSONFormatter()
	data := map[string]any{
		"key1": "value1",
		"key2": 42,
	}

	result, err := formatter.Format(data)

	require.NoError(t, err)
	assertValidJSON(t, result)
	assert.Contains(t, result, `"key1":"value1"`)
	assert.Contains(t, result, `"key2":42`)
}

func TestFormat_FormatsSlice(t *testing.T) {
	formatter := NewJSONFormatter()
	data := []testStruct{
		{ID: "id-1", Status: "pending"},
		{ID: "id-2", Status: "completed"},
	}

	result, err := formatter.Format(data)

	require.NoError(t, err)
	assertValidJSON(t, result)
	assert.Contains(t, result, `"id":"id-1"`)
	assert.Contains(t, result, `"id":"id-2"`)
}

func TestFormat_HandlesNil(t *testing.T) {
	formatter := NewJSONFormatter()

	result, err := formatter.Format(nil)

	require.NoError(t, err)
	assert.Equal(t, "null", result)
}

func TestFormat_ProducesValidJSON(t *testing.T) {
	formatter := NewJSONFormatter()
	data := map[string]any{
		"nested": map[string]any{
			"field": "value",
		},
		"array": []int{1, 2, 3},
	}

	result, err := formatter.Format(data)

	require.NoError(t, err)
	assertValidJSON(t, result)

	var parsed map[string]any
	err = json.Unmarshal([]byte(result), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "value", parsed["nested"].(map[string]any)["field"])
}

type testStruct struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func assertValidJSON(t *testing.T, jsonStr string) {
	var js any
	err := json.Unmarshal([]byte(jsonStr), &js)
	require.NoError(t, err, "String should be valid JSON")
}
