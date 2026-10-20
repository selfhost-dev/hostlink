package apiserver

import "fmt"

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Message)
}

// LogSafe renders the error without its payload. The control plane's response
// body is not logged, so a slow failure cannot put server content in the
// agent's journal.
func (e *APIError) LogSafe() string {
	return fmt.Sprintf("API error (status %d)", e.StatusCode)
}

// decodeError reports a malformed response. The underlying error may quote a
// fragment of the response, so the log-safe rendering drops it.
type decodeError struct {
	err error
}

func (e *decodeError) Error() string {
	return fmt.Sprintf("failed to decode response: %v", e.err)
}

func (e *decodeError) Unwrap() error {
	return e.err
}

func (e *decodeError) LogSafe() string {
	return "failed to decode response"
}

func (e *APIError) IsNotFound() bool {
	return e.StatusCode == 404
}

func (e *APIError) IsUnauthorised() bool {
	return e.StatusCode == 401
}
