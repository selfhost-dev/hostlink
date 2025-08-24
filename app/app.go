package app

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type App struct {
	cfg *Config
	db  *sql.DB
}

// Command represents a command in the queue
type Command struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Status    string    `json:"status"` // pending, running, completed
	Output    string    `json:"output"`
	Error     string    `json:"error"`
	ExitCode  int       `json:"exit_code,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func New(config *Config) *App {
	return &App{
		cfg: config,
	}
}

// Start will create the database
func (a *App) Start() error {
	db, err := a.openDB()
	if err != nil {
		return err
	}

	if err := a.initializeDB(db); err != nil {
		db.Close()
		return err
	}

	a.db = db

	if err := a.PrepareDB(context.Background()); err != nil {
		db.Close()
		return err
	}

	return nil
}

func (a *App) initializeDB(db *sql.DB) error {
	const vaccumSQL = "VACUUM;"
	_, err := db.Exec(vaccumSQL)
	return err
}

func (a *App) openDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", a.cfg.DBURL)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

func (a *App) Stop() {
	a.db.Close()
}
