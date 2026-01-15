package updatecheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// UpdateInfo represents the response from the update check endpoint.
type UpdateInfo struct {
	UpdateAvailable bool   `json:"update_available"`
	TargetVersion   string `json:"target_version"`
	AgentURL        string `json:"agent_url"`
	AgentSHA256     string `json:"agent_sha256"`
	AgentSize       int64  `json:"agent_size"`
	UpdaterURL      string `json:"updater_url"`
	UpdaterSHA256   string `json:"updater_sha256"`
	UpdaterSize     int64  `json:"updater_size"`
}

// RequestSignerInterface abstracts request signing for testability.
type RequestSignerInterface interface {
	SignRequest(req *http.Request) error
}

// UpdateChecker checks for available updates from the control plane.
type UpdateChecker struct {
	client          *http.Client
	controlPlaneURL string
	agentID         string
	signer          RequestSignerInterface
}

// New creates a new UpdateChecker. Returns an error if agentID is empty.
func New(client *http.Client, controlPlaneURL, agentID string, signer RequestSignerInterface) (*UpdateChecker, error) {
	if agentID == "" {
		return nil, errors.New("agentID must not be empty")
	}
	return &UpdateChecker{
		client:          client,
		controlPlaneURL: controlPlaneURL,
		agentID:         agentID,
		signer:          signer,
	}, nil
}

// Check queries the control plane for available updates.
func (c *UpdateChecker) Check(currentVersion string) (*UpdateInfo, error) {
	url := fmt.Sprintf("%s/api/v1/agents/%s/update", c.controlPlaneURL, c.agentID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Agent-Version", currentVersion)

	if c.signer != nil {
		if err := c.signer.SignRequest(req); err != nil {
			return nil, fmt.Errorf("failed to sign request: %w", err)
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update check returned status %d", resp.StatusCode)
	}

	var info UpdateInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode update check response: %w", err)
	}

	return &info, nil
}
