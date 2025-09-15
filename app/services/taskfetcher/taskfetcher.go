// Package taskfetcher fetches the tasks from external URL
package taskfetcher

import (
	"encoding/json"
	"fmt"
	"hostlink/config/appconf"
	"hostlink/config/environments/development"
	"hostlink/config/environments/production"
	"hostlink/db/schema/taskschema"
	"net/http"
	"time"
)

type TaskFetcher interface {
	Fetch() (taskschema.Task, error)
}

type taskfetcher struct {
	client *http.Client
}

func New() *taskfetcher {
	return &taskfetcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (tf taskfetcher) Fetch() ([]taskschema.Task, error) {
	apiSvrURL := ""
	if appconf.AppEnv == "development" {
		apiSvrURL = fmt.Sprintf("http://%s:%s", development.APIServerHost, development.APIServerPORT)
	} else {
		apiSvrURL = production.APIServerHost
	}

	apiSvrURL = apiSvrURL + "/api/v1/tasks"
	resp, err := tf.client.Get(apiSvrURL)
	if err != nil {
		return nil, err
	}
	var tasks []taskschema.Task
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}
