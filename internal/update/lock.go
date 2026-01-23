package update

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	// ErrLockBusy is returned when the lock is held by another live process.
	ErrLockBusy = errors.New("lock held by another process")

	// ErrLockInvalid is returned when the lock file is corrupted or has invalid format.
	ErrLockInvalid = errors.New("lock file corrupted or invalid format")

	// ErrLockNotOwned is returned when trying to release a lock not owned by this process.
	ErrLockNotOwned = errors.New("cannot release lock not owned by this process")

	// ErrLockAcquireFailed is returned when lock acquisition fails after all retries.
	ErrLockAcquireFailed = errors.New("could not acquire lock after retries")
)

// LockData represents the JSON structure stored in the lock file.
type LockData struct {
	PID            int   `json:"pid"`
	ExpireAt       int64 `json:"expire_at"`        // Unix timestamp when lock expires
	OwnerStartTime int64 `json:"owner_start_time"` // Process start time in clock ticks
}

// LockManager manages a file-based lock for coordinating updates.
type LockManager struct {
	lockPath  string
	sleepFunc func(time.Duration)
}

// LockConfig holds configuration for creating a LockManager.
type LockConfig struct {
	LockPath  string
	SleepFunc func(time.Duration) // Optional: for testing
}

// NewLockManager creates a new LockManager with the given configuration.
func NewLockManager(cfg LockConfig) *LockManager {
	sleepFunc := cfg.SleepFunc
	if sleepFunc == nil {
		sleepFunc = time.Sleep
	}
	return &LockManager{
		lockPath:  cfg.LockPath,
		sleepFunc: sleepFunc,
	}
}

// TryLock attempts to acquire the lock with the given expiration duration.
// Returns nil on success, ErrLockBusy if held by another live process,
// or another error if something else fails.
func (l *LockManager) TryLock(expiration time.Duration) error {
	// Ensure parent directory exists
	dir := filepath.Dir(l.lockPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	// Get current process info
	pid := os.Getpid()
	startTime, err := getCurrentProcessStartTime()
	if err != nil {
		return fmt.Errorf("failed to get process start time: %w", err)
	}

	// Create lock data
	lockData := LockData{
		PID:            pid,
		ExpireAt:       time.Now().Add(expiration).Unix(),
		OwnerStartTime: startTime,
	}

	content, err := json.Marshal(lockData)
	if err != nil {
		return fmt.Errorf("failed to marshal lock data: %w", err)
	}

	// Generate unique temp filename
	randSuffix, err := randomString(8)
	if err != nil {
		return err
	}
	tmpFile := l.lockPath + "." + randSuffix

	// Write to temp file
	if err := os.WriteFile(tmpFile, content, 0600); err != nil {
		return fmt.Errorf("failed to write temp lock file: %w", err)
	}

	// Try to hard link temp file to lock file (atomic operation)
	err = os.Link(tmpFile, l.lockPath)

	// Always clean up temp file
	os.Remove(tmpFile)

	if err == nil {
		// Lock acquired successfully
		return nil
	}

	// Link failed - check if existing lock is stale
	if !os.IsExist(err) {
		return fmt.Errorf("failed to create lock: %w", err)
	}

	// Read existing lock to check if it's stale
	isStale, err := l.isLockStale()
	if err != nil {
		// Corrupted or unreadable lock - treat as stale
		isStale = true
	}

	if !isStale {
		return ErrLockBusy
	}

	// Lock is stale - remove it and try again
	if err := os.Remove(l.lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove stale lock: %w", err)
	}

	// Retry acquisition with new temp file
	randSuffix, err = randomString(8)
	if err != nil {
		return err
	}
	tmpFile = l.lockPath + "." + randSuffix
	if err := os.WriteFile(tmpFile, content, 0600); err != nil {
		return fmt.Errorf("failed to write temp lock file: %w", err)
	}

	err = os.Link(tmpFile, l.lockPath)
	os.Remove(tmpFile)

	if err != nil {
		if os.IsExist(err) {
			// Another process grabbed it between our delete and link
			return ErrLockBusy
		}
		return fmt.Errorf("failed to create lock: %w", err)
	}

	return nil
}

// TryLockWithRetry attempts to acquire the lock with retries.
// It will try up to 'retries' additional times (total attempts = retries + 1),
// waiting 'interval' between each attempt.
func (l *LockManager) TryLockWithRetry(expiration time.Duration, retries int, interval time.Duration) error {
	var lastErr error

	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			l.sleepFunc(interval)
		}

		err := l.TryLock(expiration)
		if err == nil {
			return nil
		}

		lastErr = err
		if !errors.Is(err, ErrLockBusy) {
			// Non-retryable error
			return err
		}
	}

	return fmt.Errorf("%w: %v", ErrLockAcquireFailed, lastErr)
}

// Unlock releases the lock if owned by the current process.
// Returns ErrLockNotOwned if the lock is held by another process.
// Returns nil if the lock file doesn't exist (idempotent).
func (l *LockManager) Unlock() error {
	content, err := os.ReadFile(l.lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No lock to release - that's fine
			return nil
		}
		return fmt.Errorf("failed to read lock file: %w", err)
	}

	var lockData LockData
	if err := json.Unmarshal(content, &lockData); err != nil {
		// Corrupted lock file - just delete it
		if err := os.Remove(l.lockPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove corrupted lock: %w", err)
		}
		return nil
	}

	// Verify we own the lock
	if lockData.PID != os.Getpid() {
		return ErrLockNotOwned
	}

	// Delete the lock file
	if err := os.Remove(l.lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove lock: %w", err)
	}

	return nil
}

// isLockStale checks if the current lock file represents a stale lock.
// A lock is stale if:
// - It has expired (expire_at < now)
// - The owner process is dead
// - The owner PID was reused (start_time doesn't match)
func (l *LockManager) isLockStale() (bool, error) {
	content, err := os.ReadFile(l.lockPath)
	if err != nil {
		return false, err
	}

	var lockData LockData
	if err := json.Unmarshal(content, &lockData); err != nil {
		// Corrupted lock is considered stale
		return true, ErrLockInvalid
	}

	// Check if expired
	if lockData.ExpireAt < time.Now().Unix() {
		return true, nil
	}

	// Check if owner process is alive
	if !isProcessAlive(lockData.PID) {
		return true, nil
	}

	// Check for PID reuse by comparing start times
	ownerStartTime, err := getProcessStartTime(lockData.PID)
	if err != nil {
		// Can't determine start time - process might have just died
		return true, nil
	}

	if ownerStartTime != lockData.OwnerStartTime {
		// PID was reused by a different process
		return true, nil
	}

	return false, nil
}

// randomString generates a random hex string of the given length.
// Returns an error if crypto/rand fails (rare but possible in constrained environments).
func randomString(length int) (string, error) {
	bytes := make([]byte, length/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random string: %w", err)
	}
	return hex.EncodeToString(bytes)[:length], nil
}
