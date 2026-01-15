package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryLock_Success_NoExistingLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err := lm.TryLock(time.Hour)
	require.NoError(t, err)

	// Verify lock file exists
	_, err = os.Stat(lockPath)
	assert.NoError(t, err, "lock file should exist")
}

func TestTryLock_Success_CorrectFormat(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err := lm.TryLock(time.Hour)
	require.NoError(t, err)

	// Read and verify lock file content
	content, err := os.ReadFile(lockPath)
	require.NoError(t, err)

	var lockData LockData
	err = json.Unmarshal(content, &lockData)
	require.NoError(t, err)

	assert.Equal(t, os.Getpid(), lockData.PID, "PID should match current process")
	assert.Greater(t, lockData.ExpireAt, time.Now().Unix(), "expire_at should be in the future")
	assert.Greater(t, lockData.OwnerStartTime, int64(0), "owner_start_time should be positive")
}

func TestTryLock_Fail_LiveProcess(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create a lock held by current process (simulating another instance)
	startTime, err := getCurrentProcessStartTime()
	require.NoError(t, err)

	lockData := LockData{
		PID:            os.Getpid(),
		ExpireAt:       time.Now().Add(time.Hour).Unix(),
		OwnerStartTime: startTime,
	}
	content, err := json.Marshal(lockData)
	require.NoError(t, err)
	err = os.WriteFile(lockPath, content, 0600)
	require.NoError(t, err)

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err = lm.TryLock(time.Hour)
	assert.ErrorIs(t, err, ErrLockBusy)
}

func TestTryLock_Success_ExpiredLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create an expired lock
	startTime, err := getCurrentProcessStartTime()
	require.NoError(t, err)

	lockData := LockData{
		PID:            os.Getpid(),
		ExpireAt:       time.Now().Add(-time.Hour).Unix(), // Expired 1 hour ago
		OwnerStartTime: startTime,
	}
	content, err := json.Marshal(lockData)
	require.NoError(t, err)
	err = os.WriteFile(lockPath, content, 0600)
	require.NoError(t, err)

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err = lm.TryLock(time.Hour)
	assert.NoError(t, err, "should acquire lock when existing lock is expired")
}

func TestTryLock_Success_DeadPID(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create a lock held by a non-existent process
	lockData := LockData{
		PID:            99999999, // Very unlikely to exist
		ExpireAt:       time.Now().Add(time.Hour).Unix(),
		OwnerStartTime: 12345,
	}
	content, err := json.Marshal(lockData)
	require.NoError(t, err)
	err = os.WriteFile(lockPath, content, 0600)
	require.NoError(t, err)

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err = lm.TryLock(time.Hour)
	assert.NoError(t, err, "should acquire lock when owner PID is dead")
}

func TestTryLock_Success_PIDReuse(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create a lock with current PID but wrong start time (simulating PID reuse)
	lockData := LockData{
		PID:            os.Getpid(),
		ExpireAt:       time.Now().Add(time.Hour).Unix(),
		OwnerStartTime: 1, // Wrong start time - PID was reused
	}
	content, err := json.Marshal(lockData)
	require.NoError(t, err)
	err = os.WriteFile(lockPath, content, 0600)
	require.NoError(t, err)

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err = lm.TryLock(time.Hour)
	assert.NoError(t, err, "should acquire lock when PID was reused (start_time mismatch)")
}

func TestTryLock_Success_CorruptedLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create a corrupted lock file
	err := os.WriteFile(lockPath, []byte("not valid json"), 0600)
	require.NoError(t, err)

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err = lm.TryLock(time.Hour)
	assert.NoError(t, err, "should acquire lock when existing lock is corrupted")
}

func TestTryLock_TempFileCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err := lm.TryLock(time.Hour)
	require.NoError(t, err)

	// Check for any leftover temp files
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.Equal(t, "update.lock", entry.Name(), "only update.lock should remain, found: %s", entry.Name())
	}
}

func TestTryLock_CreateParentDir(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "nested", "dir", "update.lock")

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err := lm.TryLock(time.Hour)
	require.NoError(t, err)

	// Verify lock file exists
	_, err = os.Stat(lockPath)
	assert.NoError(t, err, "lock file should exist in nested directory")
}

// TryLockWithRetry tests

func TestTryLockWithRetry_Success_FirstAttempt(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	sleepCalled := 0
	mockSleep := func(d time.Duration) {
		sleepCalled++
	}

	lm := NewLockManager(LockConfig{
		LockPath:  lockPath,
		SleepFunc: mockSleep,
	})

	err := lm.TryLockWithRetry(time.Hour, 3, time.Second)
	require.NoError(t, err)
	assert.Equal(t, 0, sleepCalled, "should not sleep when lock acquired on first attempt")
}

func TestTryLockWithRetry_Success_ThirdAttempt(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	attempts := 0
	sleepCalled := 0
	mockSleep := func(d time.Duration) {
		sleepCalled++
		// After 2 failed attempts, release the lock
		if sleepCalled == 2 {
			os.Remove(lockPath)
		}
	}

	// Create initial lock held by "another" process (dead PID won't work, use current PID)
	startTime, _ := getCurrentProcessStartTime()
	lockData := LockData{
		PID:            os.Getpid(),
		ExpireAt:       time.Now().Add(time.Hour).Unix(),
		OwnerStartTime: startTime,
	}
	content, _ := json.Marshal(lockData)
	os.WriteFile(lockPath, content, 0600)

	lm := NewLockManager(LockConfig{
		LockPath:  lockPath,
		SleepFunc: mockSleep,
	})

	// Track actual attempts via the mock
	_ = attempts

	err := lm.TryLockWithRetry(time.Hour, 5, time.Second)
	require.NoError(t, err)
	assert.Equal(t, 2, sleepCalled, "should sleep between retries until lock acquired")
}

func TestTryLockWithRetry_Fail_MaxRetries(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	sleepCalled := 0
	mockSleep := func(d time.Duration) {
		sleepCalled++
	}

	// Create a lock that will never be released (held by current process)
	startTime, _ := getCurrentProcessStartTime()
	lockData := LockData{
		PID:            os.Getpid(),
		ExpireAt:       time.Now().Add(time.Hour).Unix(),
		OwnerStartTime: startTime,
	}
	content, _ := json.Marshal(lockData)
	os.WriteFile(lockPath, content, 0600)

	lm := NewLockManager(LockConfig{
		LockPath:  lockPath,
		SleepFunc: mockSleep,
	})

	err := lm.TryLockWithRetry(time.Hour, 3, time.Second)
	assert.ErrorIs(t, err, ErrLockAcquireFailed)
	assert.Equal(t, 3, sleepCalled, "should sleep between each retry")
}

func TestTryLockWithRetry_RetryInterval(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	var sleepDurations []time.Duration
	mockSleep := func(d time.Duration) {
		sleepDurations = append(sleepDurations, d)
	}

	// Create a lock that will never be released
	startTime, _ := getCurrentProcessStartTime()
	lockData := LockData{
		PID:            os.Getpid(),
		ExpireAt:       time.Now().Add(time.Hour).Unix(),
		OwnerStartTime: startTime,
	}
	content, _ := json.Marshal(lockData)
	os.WriteFile(lockPath, content, 0600)

	lm := NewLockManager(LockConfig{
		LockPath:  lockPath,
		SleepFunc: mockSleep,
	})

	expectedInterval := 500 * time.Millisecond
	_ = lm.TryLockWithRetry(time.Hour, 2, expectedInterval)

	for i, d := range sleepDurations {
		assert.Equal(t, expectedInterval, d, "sleep duration %d should match interval", i)
	}
}

// Unlock tests

func TestUnlock_Success_Owner(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	// Acquire lock first
	err := lm.TryLock(time.Hour)
	require.NoError(t, err)

	// Release it
	err = lm.Unlock()
	require.NoError(t, err)

	// Verify lock file is gone
	_, err = os.Stat(lockPath)
	assert.True(t, os.IsNotExist(err), "lock file should be deleted")
}

func TestUnlock_Fail_NotOwner(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create a lock held by another PID
	lockData := LockData{
		PID:            99999999,
		ExpireAt:       time.Now().Add(time.Hour).Unix(),
		OwnerStartTime: 12345,
	}
	content, _ := json.Marshal(lockData)
	os.WriteFile(lockPath, content, 0600)

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	err := lm.Unlock()
	assert.ErrorIs(t, err, ErrLockNotOwned)

	// Verify lock file still exists
	_, err = os.Stat(lockPath)
	assert.NoError(t, err, "lock file should still exist")
}

func TestUnlock_Success_NoLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	// Unlock when no lock exists should be idempotent
	err := lm.Unlock()
	assert.NoError(t, err, "unlocking non-existent lock should succeed")
}

func TestUnlock_Success_CorruptedLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create a corrupted lock file
	err := os.WriteFile(lockPath, []byte("corrupted"), 0600)
	require.NoError(t, err)

	lm := NewLockManager(LockConfig{LockPath: lockPath})

	// Should delete corrupted lock file
	err = lm.Unlock()
	assert.NoError(t, err, "should handle corrupted lock gracefully")

	// Verify lock file is gone
	_, err = os.Stat(lockPath)
	assert.True(t, os.IsNotExist(err), "corrupted lock file should be deleted")
}
