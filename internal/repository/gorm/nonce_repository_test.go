package gorm

import (
	"context"
	"fmt"
	"hostlink/domain/nonce"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestNonceRepository_Save(t *testing.T) {
	t.Run("should save nonce to database", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		n := nonce.NewNonce()

		err := repo.Save(ctx, n)
		if err != nil {
			t.Fatalf("Failed to save nonce: %v", err)
		}

		// Verify nonce was saved by finding it
		found, err := repo.FindByValue(ctx, n.Value)
		if err != nil {
			t.Fatalf("Failed to find saved nonce: %v", err)
		}

		if found.Value != n.Value {
			t.Errorf("Expected nonce value %s, got %s", n.Value, found.Value)
		}

		if found.CreatedAt.IsZero() {
			t.Error("CreatedAt should not be zero after saving")
		}
	})

	t.Run("should enforce unique nonce constraint", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		n := createTestNonce("unique-nonce-123")

		// Save the first nonce
		err := repo.Save(ctx, n)
		if err != nil {
			t.Fatalf("Failed to save first nonce: %v", err)
		}

		// Try to save a duplicate nonce
		duplicate := createTestNonce("unique-nonce-123")
		err = repo.Save(ctx, duplicate)

		// Should return an error for duplicate
		if err == nil {
			t.Fatal("Expected error when saving duplicate nonce, got nil")
		}

		// Verify only one nonce exists in database
		exists, err := repo.Exists(ctx, "unique-nonce-123")
		if err != nil {
			t.Fatalf("Failed to check nonce existence: %v", err)
		}
		if !exists {
			t.Error("Original nonce should still exist")
		}
	})

	t.Run("should handle concurrent saves safely", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Verify starting with empty database
		initialCount, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get initial count: %v", err)
		}
		if initialCount != 0 {
			t.Fatalf("Expected empty database, got %d nonces", initialCount)
		}

		numGoroutines := 10
		numNoncesPerGoroutine := 5
		expectedTotal := numGoroutines * numNoncesPerGoroutine

		// Track all nonces we create
		nonceChan := make(chan string, expectedTotal)
		errChan := make(chan error, expectedTotal)
		doneChan := make(chan bool, numGoroutines)

		for i := range numGoroutines {
			go func(goroutineID int) {
				for range numNoncesPerGoroutine {
					n := nonce.NewNonce()
					if err := repo.Save(ctx, n); err != nil {
						errChan <- err
					} else {
						nonceChan <- n.Value
					}
				}
				doneChan <- true
			}(i)
		}

		// Wait for all goroutines to complete
		for range numGoroutines {
			<-doneChan
		}
		close(errChan)
		close(nonceChan)

		// Check for any errors
		for err := range errChan {
			t.Errorf("Concurrent save error: %v", err)
		}

		// Collect all saved nonces
		var savedNonces []string
		for nonce := range nonceChan {
			savedNonces = append(savedNonces, nonce)
		}

		// Verify the correct number of nonces were saved to database
		finalCount, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get final count: %v", err)
		}
		if finalCount != int64(expectedTotal) {
			t.Errorf("Expected %d nonces in database, got %d", expectedTotal, finalCount)
		}

		// Verify we tracked the correct number of saves
		if len(savedNonces) != expectedTotal {
			t.Errorf("Expected %d nonces to be tracked, got %d", expectedTotal, len(savedNonces))
		}

		// Verify all tracked nonces exist in the database
		for _, nonceValue := range savedNonces {
			exists, err := repo.Exists(ctx, nonceValue)
			if err != nil {
				t.Errorf("Failed to check existence of nonce %s: %v", nonceValue, err)
			}
			if !exists {
				t.Errorf("Nonce %s was saved but doesn't exist in database", nonceValue)
			}
		}
	})
}

func TestNonceRepository_Count(t *testing.T) {
	t.Run("should return zero for empty database", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		count, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get count: %v", err)
		}

		if count != 0 {
			t.Errorf("Expected count 0 for empty database, got %d", count)
		}
	})

	t.Run("should return correct count after adding nonces", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Add 5 nonces
		nonces := generateTestNonces(5)
		for _, n := range nonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save nonce: %v", err)
			}
		}

		count, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get count: %v", err)
		}

		if count != 5 {
			t.Errorf("Expected count 5, got %d", count)
		}
	})

	t.Run("should update count after deleting nonces", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Add some nonces that will be expired (older than 5 minutes) for deletion
		// but some that are still valid for initial count
		oldTime := time.Now().Add(-10 * time.Minute)
		veryOldNonces := []*nonce.Nonce{
			createTestNonceWithTime("veryold1", oldTime),
			createTestNonceWithTime("veryold2", oldTime),
			createTestNonceWithTime("veryold3", oldTime),
		}

		// Add nonces that are less than 5 minutes old (will be counted)
		recentTime := time.Now().Add(-3 * time.Minute)
		recentNonces := []*nonce.Nonce{
			createTestNonceWithTime("recent1", recentTime),
			createTestNonceWithTime("recent2", recentTime),
		}

		// Add fresh nonces
		newNonces := generateTestNonces(2)

		// Save all nonces
		for _, n := range veryOldNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save very old nonce: %v", err)
			}
		}
		for _, n := range recentNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save recent nonce: %v", err)
			}
		}
		for _, n := range newNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save new nonce: %v", err)
			}
		}

		// Verify initial count (should only count non-expired: 2 recent + 2 new = 4)
		count, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get initial count: %v", err)
		}
		if count != 4 {
			t.Errorf("Expected initial count 4 (non-expired), got %d", count)
		}

		// Delete expired nonces (older than 5 minutes)
		deleted, err := repo.DeleteExpired(ctx, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to delete expired nonces: %v", err)
		}
		if deleted != 3 {
			t.Errorf("Expected 3 deleted nonces, got %d", deleted)
		}

		// Verify count after deletion (still 4, since we only deleted already-expired ones)
		count, err = repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get count after deletion: %v", err)
		}
		if count != 4 {
			t.Errorf("Expected count 4 after deletion (unchanged), got %d", count)
		}
	})

	t.Run("should handle count with large number of nonces", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Add 1000 nonces
		numNonces := 1000
		batchSize := 100

		for i := 0; i < numNonces; i += batchSize {
			batch := generateTestNonces(batchSize)
			for _, n := range batch {
				if err := repo.Save(ctx, n); err != nil {
					t.Fatalf("Failed to save nonce: %v", err)
				}
			}
		}

		count, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get count: %v", err)
		}

		if count != int64(numNonces) {
			t.Errorf("Expected count %d, got %d", numNonces, count)
		}
	})

	t.Run("should exclude expired nonces from count", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Add expired nonces (older than 5 minutes)
		expiredTime := time.Now().Add(-6 * time.Minute)
		expiredNonces := []*nonce.Nonce{
			createTestNonceWithTime("expired1", expiredTime),
			createTestNonceWithTime("expired2", expiredTime),
			createTestNonceWithTime("expired3", expiredTime),
		}

		// Add valid nonces (less than 5 minutes old)
		recentTime := time.Now().Add(-2 * time.Minute)
		validNonces := []*nonce.Nonce{
			createTestNonceWithTime("valid1", recentTime),
			createTestNonceWithTime("valid2", recentTime),
		}

		// Add fresh nonces
		freshNonces := generateTestNonces(3)

		// Save all nonces
		for _, n := range expiredNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save expired nonce: %v", err)
			}
		}
		for _, n := range validNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save valid nonce: %v", err)
			}
		}
		for _, n := range freshNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save fresh nonce: %v", err)
			}
		}

		// Count should only return non-expired nonces (5 total: 2 valid + 3 fresh)
		count, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get count: %v", err)
		}

		if count != 5 {
			t.Errorf("Expected count 5 (excluding expired), got %d", count)
		}
	})
}

func TestNonceRepository_FindByValue(t *testing.T) {
	t.Run("should find existing nonce by value", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Create and save a nonce
		original := createTestNonce("test-nonce-abc123")
		err := repo.Save(ctx, original)
		if err != nil {
			t.Fatalf("Failed to save nonce: %v", err)
		}

		// Find the nonce by value
		found, err := repo.FindByValue(ctx, "test-nonce-abc123")
		if err != nil {
			t.Fatalf("Failed to find nonce: %v", err)
		}

		// Verify the found nonce matches
		if found.Value != original.Value {
			t.Errorf("Expected nonce value %s, got %s", original.Value, found.Value)
		}

		if found.CreatedAt.IsZero() {
			t.Error("CreatedAt should not be zero")
		}

		// Verify timestamps are close (within 1 second tolerance for DB precision)
		timeDiff := found.CreatedAt.Sub(original.CreatedAt).Abs()
		if timeDiff > time.Second {
			t.Errorf("CreatedAt timestamps differ too much: %v", timeDiff)
		}
	})

	t.Run("should return error when nonce not found", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Try to find a non-existent nonce
		found, err := repo.FindByValue(ctx, "non-existent-nonce")

		// Should return an error
		if err == nil {
			t.Fatal("Expected error when finding non-existent nonce, got nil")
		}

		// Should return nil nonce
		if found != nil {
			t.Errorf("Expected nil nonce, got %+v", found)
		}

		// Verify it's specifically a "record not found" error from gorm
		if err.Error() != "record not found" {
			t.Errorf("Expected 'record not found' error, got: %v", err)
		}
	})
}

func TestNonceRepository_Exists(t *testing.T) {
	t.Run("should return true when nonce exists", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Create and save a nonce
		n := createTestNonce("exists-test-123")
		err := repo.Save(ctx, n)
		if err != nil {
			t.Fatalf("Failed to save nonce: %v", err)
		}

		// Check if nonce exists
		exists, err := repo.Exists(ctx, "exists-test-123")
		if err != nil {
			t.Fatalf("Failed to check nonce existence: %v", err)
		}

		if !exists {
			t.Error("Expected nonce to exist, but Exists returned false")
		}
	})

	t.Run("should return false when nonce doesn't exist", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Check if a non-existent nonce exists
		exists, err := repo.Exists(ctx, "does-not-exist-456")
		if err != nil {
			t.Fatalf("Failed to check nonce existence: %v", err)
		}

		if exists {
			t.Error("Expected nonce to not exist, but Exists returned true")
		}
	})

	t.Run("should be case-sensitive", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Save a nonce with mixed case
		n := createTestNonce("CaseSensitive123")
		err := repo.Save(ctx, n)
		if err != nil {
			t.Fatalf("Failed to save nonce: %v", err)
		}

		// Check with exact case - should exist
		exists, err := repo.Exists(ctx, "CaseSensitive123")
		if err != nil {
			t.Fatalf("Failed to check nonce existence: %v", err)
		}
		if !exists {
			t.Error("Expected nonce to exist with exact case match")
		}

		// Check with lowercase - should NOT exist
		exists, err = repo.Exists(ctx, "casesensitive123")
		if err != nil {
			t.Fatalf("Failed to check nonce existence: %v", err)
		}
		if exists {
			t.Error("Expected nonce to NOT exist with lowercase, database should be case-sensitive")
		}

		// Check with uppercase - should NOT exist
		exists, err = repo.Exists(ctx, "CASESENSITIVE123")
		if err != nil {
			t.Fatalf("Failed to check nonce existence: %v", err)
		}
		if exists {
			t.Error("Expected nonce to NOT exist with uppercase, database should be case-sensitive")
		}
	})
}

func TestNonceRepository_DeleteExpired(t *testing.T) {
	t.Run("should delete nonces older than specified time", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Create nonces with different ages
		oldTime1 := time.Now().Add(-10 * time.Minute)
		oldTime2 := time.Now().Add(-7 * time.Minute)
		recentTime := time.Now().Add(-3 * time.Minute)
		currentTime := time.Now()

		oldNonces := []*nonce.Nonce{
			createTestNonceWithTime("old1", oldTime1),
			createTestNonceWithTime("old2", oldTime2),
		}

		newNonces := []*nonce.Nonce{
			createTestNonceWithTime("recent", recentTime),
			createTestNonceWithTime("current", currentTime),
		}

		// Save all nonces
		for _, n := range oldNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save old nonce: %v", err)
			}
		}
		for _, n := range newNonces {
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save new nonce: %v", err)
			}
		}

		// Delete nonces older than 5 minutes
		deletedCount, err := repo.DeleteExpired(ctx, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to delete expired nonces: %v", err)
		}

		// Should have deleted 2 old nonces
		if deletedCount != 2 {
			t.Errorf("Expected 2 deleted nonces, got %d", deletedCount)
		}

		// Verify old nonces are gone
		for _, n := range oldNonces {
			exists, err := repo.Exists(ctx, n.Value)
			if err != nil {
				t.Fatalf("Failed to check existence: %v", err)
			}
			if exists {
				t.Errorf("Old nonce %s should have been deleted", n.Value)
			}
		}

		// Verify new nonces still exist
		for _, n := range newNonces {
			exists, err := repo.Exists(ctx, n.Value)
			if err != nil {
				t.Fatalf("Failed to check existence: %v", err)
			}
			if !exists {
				t.Errorf("Recent nonce %s should still exist", n.Value)
			}
		}
	})

	t.Run("should keep nonces newer than specified time", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Create all recent nonces (all less than 5 minutes old)
		times := []time.Duration{
			-4 * time.Minute,
			-3 * time.Minute,
			-2 * time.Minute,
			-1 * time.Minute,
			0, // current time
		}

		var nonces []*nonce.Nonce
		for i, duration := range times {
			timestamp := time.Now().Add(duration)
			n := createTestNonceWithTime(fmt.Sprintf("recent%d", i), timestamp)
			nonces = append(nonces, n)
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save nonce: %v", err)
			}
		}

		// Try to delete nonces older than 5 minutes
		deletedCount, err := repo.DeleteExpired(ctx, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to delete expired nonces: %v", err)
		}

		// Should have deleted 0 nonces since all are recent
		if deletedCount != 0 {
			t.Errorf("Expected 0 deleted nonces, got %d", deletedCount)
		}

		// Verify all nonces still exist
		for _, n := range nonces {
			exists, err := repo.Exists(ctx, n.Value)
			if err != nil {
				t.Fatalf("Failed to check existence: %v", err)
			}
			if !exists {
				t.Errorf("Recent nonce %s should still exist", n.Value)
			}
		}
	})

	t.Run("should return count of deleted nonces", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Create a mix of old and new nonces
		oldTime := time.Now().Add(-10 * time.Minute)
		recentTime := time.Now().Add(-2 * time.Minute)

		// Create 5 old nonces
		for i := range 5 {
			n := createTestNonceWithTime(fmt.Sprintf("old%d", i), oldTime)
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save old nonce: %v", err)
			}
		}

		// Create 3 recent nonces
		for i := range 3 {
			n := createTestNonceWithTime(fmt.Sprintf("recent%d", i), recentTime)
			if err := repo.Save(ctx, n); err != nil {
				t.Fatalf("Failed to save recent nonce: %v", err)
			}
		}

		// Delete nonces older than 5 minutes
		deletedCount, err := repo.DeleteExpired(ctx, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to delete expired nonces: %v", err)
		}

		// Should return exactly 5 as the count
		if deletedCount != 5 {
			t.Errorf("Expected deleted count to be 5, got %d", deletedCount)
		}

		// Delete again - should return 0 since already deleted
		deletedCount2, err := repo.DeleteExpired(ctx, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to delete expired nonces second time: %v", err)
		}

		if deletedCount2 != 0 {
			t.Errorf("Expected deleted count to be 0 on second delete, got %d", deletedCount2)
		}
	})

	t.Run("should handle empty table gracefully", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Try to delete from empty table
		deletedCount, err := repo.DeleteExpired(ctx, 5*time.Minute)
		if err != nil {
			t.Fatalf("Failed to delete from empty table: %v", err)
		}

		// Should return 0 and no error
		if deletedCount != 0 {
			t.Errorf("Expected 0 deleted from empty table, got %d", deletedCount)
		}

		// Verify count is still 0
		count, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get count: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected count to remain 0, got %d", count)
		}
	})
}

func TestNonceRepository_Transaction(t *testing.T) {
	t.Run("should rollback on error", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Create an initial nonce
		initialNonce := createTestNonce("initial")
		err := repo.Save(ctx, initialNonce)
		if err != nil {
			t.Fatalf("Failed to save initial nonce: %v", err)
		}

		// Start a transaction that will fail
		err = repo.Transaction(ctx, func(txRepo *NonceRepository) error {
			// Create a new nonce within transaction
			txNonce := createTestNonce("in-transaction")
			if err := txRepo.Save(ctx, txNonce); err != nil {
				return err
			}

			// Verify nonce exists within transaction
			exists, err := txRepo.Exists(ctx, "in-transaction")
			if err != nil {
				return err
			}
			if !exists {
				t.Error("Expected nonce to exist within transaction")
			}

			// Return error to force rollback
			return fmt.Errorf("intentional error for rollback")
		})

		// Transaction should have returned the error
		if err == nil {
			t.Fatal("Expected transaction to return error")
		}
		if err.Error() != "intentional error for rollback" {
			t.Errorf("Expected 'intentional error for rollback', got: %v", err)
		}

		// Verify the transaction nonce was rolled back
		exists, err := repo.Exists(ctx, "in-transaction")
		if err != nil {
			t.Fatalf("Failed to check existence: %v", err)
		}
		if exists {
			t.Error("Transaction nonce should not exist after rollback")
		}

		// Verify initial nonce still exists
		exists, err = repo.Exists(ctx, "initial")
		if err != nil {
			t.Fatalf("Failed to check initial nonce: %v", err)
		}
		if !exists {
			t.Error("Initial nonce should still exist")
		}
	})

	t.Run("should commit on success", func(t *testing.T) {
		repo, cleanup := setupNonceTestDB(t)
		defer cleanup()
		ctx := context.Background()

		// Verify database is empty
		initialCount, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get initial count: %v", err)
		}
		if initialCount != 0 {
			t.Fatalf("Expected empty database, got %d nonces", initialCount)
		}

		// Start a successful transaction
		err = repo.Transaction(ctx, func(txRepo *NonceRepository) error {
			// Create multiple nonces within transaction
			nonces := []string{"tx-nonce-1", "tx-nonce-2", "tx-nonce-3"}

			for _, value := range nonces {
				n := createTestNonce(value)
				if err := txRepo.Save(ctx, n); err != nil {
					return fmt.Errorf("failed to save nonce %s: %w", value, err)
				}
			}

			// Verify count within transaction
			count, err := txRepo.Count(ctx)
			if err != nil {
				return fmt.Errorf("failed to count in transaction: %w", err)
			}
			if count != 3 {
				t.Errorf("Expected 3 nonces in transaction, got %d", count)
			}

			// Return nil to commit
			return nil
		})

		// Transaction should succeed
		if err != nil {
			t.Fatalf("Expected transaction to succeed, got error: %v", err)
		}

		// Verify all nonces were committed
		finalCount, err := repo.Count(ctx)
		if err != nil {
			t.Fatalf("Failed to get final count: %v", err)
		}
		if finalCount != 3 {
			t.Errorf("Expected 3 nonces after commit, got %d", finalCount)
		}

		// Verify each nonce exists
		expectedNonces := []string{"tx-nonce-1", "tx-nonce-2", "tx-nonce-3"}
		for _, value := range expectedNonces {
			exists, err := repo.Exists(ctx, value)
			if err != nil {
				t.Fatalf("Failed to check existence of %s: %v", value, err)
			}
			if !exists {
				t.Errorf("Expected nonce %s to exist after commit", value)
			}
		}
	})
}

// Helper functions for tests
func setupNonceTestDB(t *testing.T) (*NonceRepository, func()) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	err = db.AutoMigrate(&nonce.Nonce{})
	if err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	repo := NewNonceRepository(db)

	cleanup := func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}

	return repo, cleanup
}

func createTestNonce(value string) *nonce.Nonce {
	if value == "" {
		return nonce.NewNonce()
	}
	return &nonce.Nonce{
		Value:     value,
		CreatedAt: time.Now(),
	}
}

func createTestNonceWithTime(value string, createdAt time.Time) *nonce.Nonce {
	return &nonce.Nonce{
		Value:     value,
		CreatedAt: createdAt,
	}
}

func insertTestNonces(t *testing.T, repo *NonceRepository, nonces ...*nonce.Nonce) error {
	t.Helper()
	ctx := context.Background()
	for _, n := range nonces {
		if err := repo.Save(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

func generateTestNonces(count int) []*nonce.Nonce {
	nonces := make([]*nonce.Nonce, count)
	for i := range count {
		nonces[i] = nonce.NewNonce()
	}
	return nonces
}
