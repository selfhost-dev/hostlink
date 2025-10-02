package taskjob

import (
	"hostlink/app/services/taskfetcher"
	"hostlink/domain/task"
	"time"

	"github.com/labstack/gommon/log"
)

func Trigger(fn func(tsk []task.Task) error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	tfetcher, err := taskfetcher.NewDefault()
	if err != nil {
		log.Error("failed to initialize task fetcher", err)
		return
	}

	for range ticker.C {
		allTasks, err := tfetcher.Fetch()
		if err != nil {
			continue
		}
		incompleteTasks := []task.Task{}
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
