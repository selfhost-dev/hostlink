package taskjob

import (
	"hostlink/app/services/taskfetcher"
	"hostlink/db/schema/taskschema"
	"time"

	"github.com/labstack/gommon/log"
)

func Trigger(fn func(tsk []taskschema.Task) error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	tfetcher := taskfetcher.New()
	for range ticker.C {
		allTasks, err := tfetcher.Fetch()
		if err != nil {
			continue
		}
		incompleteTasks := []taskschema.Task{}
		for _, task := range allTasks {
			if task.Status != "completed" {
				incompleteTasks = append(incompleteTasks, task)
			}
		}
		if err := fn(incompleteTasks); err != nil {
			log.Error("error while running callback", err)
			continue
		}
	}
}
