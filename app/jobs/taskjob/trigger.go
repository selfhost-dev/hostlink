package taskjob

import (
	"hostlink/app/services/taskfetcher"
	"hostlink/db/schema/taskschema"
	"hostlink/internal/dbconn"
	gormrepo "hostlink/internal/repository/gorm"
	"time"

	"github.com/labstack/gommon/log"
)

func Trigger(fn func(tsk []taskschema.Task) error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	db, err := dbconn.GetConn()
	if err != nil {
		log.Error("failed to get database connection", err)
		return
	}

	nonceRepo := gormrepo.NewNonceRepository(db)
	tfetcher, err := taskfetcher.NewDefault(nonceRepo)
	if err != nil {
		log.Error("failed to initialize task fetcher", err)
		return
	}

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
