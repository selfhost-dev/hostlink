package app

import (
	"database/sql"
	"hostlink/config"

	_ "github.com/mattn/go-sqlite3"
)

type App struct {
	cfg *config.Config
	db  *sql.DB
}

func New(config *config.Config) *App {
	return &App{
		cfg: config,
	}
}

// Start will create the database
func (a *App) Start() error {
	db, err := a.createSqliteDB()
	if err != nil {
		return err
	}
	a.db = db
	return nil
}

func (a *App) createSqliteDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", a.cfg.DBURL)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	initWithVaccume := `VACUUM;`

	if _, err := db.Exec(initWithVaccume); err != nil {
		return nil, err
	}

	return db, nil
}

func (a *App) Stop() {
	a.db.Close()
}
