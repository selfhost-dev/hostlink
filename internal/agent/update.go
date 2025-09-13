package agent

import "hostlink/version"

func (a Agent) GetCurrentVersion() string {
	return version.Version
}
