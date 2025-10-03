package output

import "encoding/json"

// Formatter interface for formatting output
type Formatter interface {
	Format(data any) (string, error)
}

// JSONFormatter implements the Formatter interface for JSON output
type JSONFormatter struct{}

// NewJSONFormatter creates a new JSON formatter
func NewJSONFormatter() *JSONFormatter {
	return &JSONFormatter{}
}

// Format formats data as JSON
func (f *JSONFormatter) Format(data any) (string, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
