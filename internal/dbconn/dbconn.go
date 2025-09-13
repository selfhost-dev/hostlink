// Package dbconn holds releated about the connection to the SQLite database.
// It tries to be a single db connection
package dbconn

import (
	"errors"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var db *gorm.DB

type DBConf struct {
	URL         string
	MaxIdle     int
	MaxOpen     int
	MaxLifetime time.Duration
}

type DBOpts func(*DBConf)

func NewConf() *DBConf {
	return &DBConf{
		URL:         "file:selfhost.db",
		MaxIdle:     25,
		MaxOpen:     25,
		MaxLifetime: 300 * time.Second,
	}
}

func WithURL(url string) DBOpts {
	return func(d *DBConf) {
		d.URL = url
	}
}

func WithMaxIdle(idle int) DBOpts {
	return func(d *DBConf) {
		d.MaxIdle = idle
	}
}

func WithMaxOpen(open int) DBOpts {
	return func(d *DBConf) {
		d.MaxOpen = open
	}
}

func WithMaxLifetime(lifetime time.Duration) DBOpts {
	return func(d *DBConf) {
		d.MaxLifetime = lifetime
	}
}

// GetConn provide the connection link to the db
// TODO: Make it thread safe
func GetConn(options ...DBOpts) (*gorm.DB, error) {
	if db != nil {
		return db, nil
	}

	dbConf := NewConf()
	for _, o := range options {
		o(dbConf)
	}

	var err error
	db, err = gorm.Open(sqlite.Open(dbConf.URL), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	sdb, err := db.DB()
	if err != nil {
		return nil, err
	}

	sdb.SetMaxIdleConns(dbConf.MaxIdle)
	sdb.SetMaxOpenConns(dbConf.MaxOpen)
	sdb.SetConnMaxLifetime(dbConf.MaxLifetime)

	if err := sdb.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

func Migrate[T any](t T) error {
	if db == nil {
		return errors.New("db is not defined")
	}
	return db.AutoMigrate(t)
}

func Close() error {
	if db != nil {
		if sdb, err := db.DB(); err != nil {
			db = nil
			return err
		} else {
			db = nil
			return sdb.Close()
		}
	}
	return nil
}
