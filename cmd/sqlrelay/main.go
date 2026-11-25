// sqlrelay passes the SQL stream path's frames between browser tabs and agents
// (internal/sqlstream/relay). It runs next to the selfhost backend, which signs every token
// and ticket it checks; it stores nothing, so a restart only makes agents and tabs reconnect.
//
// Environment:
//
//	SQLRELAY_LISTEN           address to listen on (":8090")
//	SQLRELAY_KEYS             the backend's public keys, "kid:<base64url>,..." , or
//	SQLRELAY_KEYS_URL         where the backend publishes them (GET /api/v1/sql_stream/keys),
//	                          read at start and every 10 minutes
//	SQLRELAY_ALLOWED_ORIGINS  the console's origins, comma-separated
//	SQLRELAY_LOG_LEVEL        logrus level ("info")
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"hostlink/internal/sqlstream/relay"
	"hostlink/internal/sqlstream/token"
)

func main() {
	log.SetFormatter(&log.JSONFormatter{})
	if level, err := log.ParseLevel(envOr("SQLRELAY_LOG_LEVEL", "info")); err == nil {
		log.SetLevel(level)
	}
	if err := run(); err != nil {
		log.WithError(err).Fatal("sqlrelay stopped")
	}
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func run() error {
	keys, refresh, err := loadKeys()
	if err != nil {
		return err
	}
	origins := []string{}
	for _, o := range strings.Split(os.Getenv("SQLRELAY_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		return errors.New("SQLRELAY_ALLOWED_ORIGINS is empty: no console could connect")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if refresh != nil {
		go refresh(ctx)
	}

	srv := relay.New(relay.Config{Keys: keys, AllowedOrigins: origins})
	httpServer := &http.Server{
		Addr:              envOr("SQLRELAY_LISTEN", ":8090"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() { errs <- httpServer.ListenAndServe() }()
	log.WithFields(log.Fields{"addr": httpServer.Addr, "keys": keys.Len(), "origins": len(origins)}).Info("sqlrelay listening")

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	srv.Shutdown()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

// loadKeys reads the keys from SQLRELAY_KEYS, or from SQLRELAY_KEYS_URL with a refresher that
// keeps the last good set when a read fails.
func loadKeys() (*token.KeySet, func(context.Context), error) {
	if spec := os.Getenv("SQLRELAY_KEYS"); spec != "" {
		keys, err := token.ParseKeys(spec)
		if err != nil {
			return nil, nil, fmt.Errorf("SQLRELAY_KEYS: %w", err)
		}
		return token.NewKeySet(keys), nil, nil
	}
	url := os.Getenv("SQLRELAY_KEYS_URL")
	if url == "" {
		return nil, nil, errors.New("set SQLRELAY_KEYS or SQLRELAY_KEYS_URL")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	read := func() (map[string]ed25519.PublicKey, error) {
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("keys: HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if err != nil {
			return nil, err
		}
		return token.ParseJWKS(body)
	}
	var first map[string]ed25519.PublicKey
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if first, err = read(); err == nil {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", url, err)
	}
	keys := token.NewKeySet(first)
	refresh := func(ctx context.Context) {
		tick := time.NewTicker(10 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				fresh, err := read()
				if err != nil {
					log.WithError(err).Warn("keys not refreshed; keeping the last good set")
					continue
				}
				keys.Replace(fresh)
			}
		}
	}
	return keys, refresh, nil
}
