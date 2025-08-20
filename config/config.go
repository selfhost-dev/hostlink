package config

import "sync"

type Config struct {
	mu    sync.Mutex
	DBURL string
}

func New() *Config {
	return &Config{
		DBURL: "file:hostlink.sqlite3",
	}
}

// WithDBURL will specify the path of the SQLite database
func (c *Config) WithDBURL(url string) *Config {
	c.mu.Lock()
	c.DBURL = url
	c.mu.Unlock()
	return c
}
