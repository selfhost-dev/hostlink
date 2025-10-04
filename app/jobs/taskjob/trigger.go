package taskjob

import (
	"time"

	"github.com/labstack/gommon/log"
)

func Trigger(fn func() error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := fn(); err != nil {
			log.Error("error while running callback", err)
			continue
		}
	}
}
