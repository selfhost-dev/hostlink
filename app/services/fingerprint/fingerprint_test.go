package fingerprint

import (
	"encoding/json"
	"fmt"
	"hostlink/internal/sysinfo"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewManager(t *testing.T) {
	t.Run("should create manager with correct path", func(t *testing.T) {
		expectedPath := "/var/lib/hostlink/fingerprint.json"

		manager := NewManager(expectedPath)

		if manager == nil {
			t.Fatal("NewManager returned nil")
		}

		if manager.fingerprintPath != expectedPath {
			t.Errorf("Expected fingerprintPath %s, got %s", expectedPath, manager.fingerprintPath)
		}
	})

	t.Run("should set default threshold to 40", func(t *testing.T) {
		path := "/tmp/test-fingerprint.json"

		manager := NewManager(path)

		if manager == nil {
			t.Fatal("NewManager returned nil")
		}

		if manager.threshold != 40 {
			t.Errorf("Expected default threshold 40, got %d", manager.threshold)
		}
	})
}

func TestLoadOrGenerate(t *testing.T) {
	t.Run("should generate new fingerprint when file doesn't exist", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		data, isNew, err := manager.LoadOrGenerate()

		if err != nil {
			t.Fatalf("LoadOrGenerate failed: %v", err)
		}

		if !isNew {
			t.Error("Expected isNew to be true for newly generated fingerprint")
		}

		if data == nil {
			t.Fatal("Expected non-nil FingerprintData")
		}

		if data.Fingerprint == "" {
			t.Error("Expected non-empty fingerprint")
		}

		// Verify it's a valid UUID
		if _, err := uuid.Parse(data.Fingerprint); err != nil {
			t.Errorf("Fingerprint is not a valid UUID: %v", err)
		}

		if data.HardwareHash == nil {
			t.Error("Expected non-nil HardwareHash")
		}

		if data.SimilarityThreshold != 40 {
			t.Errorf("Expected SimilarityThreshold 40, got %d", data.SimilarityThreshold)
		}

		// Verify file was created
		if _, err := os.Stat(fingerprintPath); os.IsNotExist(err) {
			t.Error("Fingerprint file was not created")
		}
	})

	t.Run("should load existing fingerprint when file exists", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// First, generate a fingerprint
		originalData, _, err := manager.LoadOrGenerate()
		if err != nil {
			t.Fatalf("Failed to generate initial fingerprint: %v", err)
		}

		// Create a new manager to simulate loading existing data
		manager2 := NewManager(fingerprintPath)

		// Load the existing fingerprint
		loadedData, isNew, err := manager2.LoadOrGenerate()

		if err != nil {
			t.Fatalf("LoadOrGenerate failed: %v", err)
		}

		if isNew {
			t.Error("Expected isNew to be false for existing fingerprint")
		}

		if loadedData == nil {
			t.Fatal("Expected non-nil FingerprintData")
		}

		// The fingerprint should be the same as the original
		if loadedData.Fingerprint != originalData.Fingerprint {
			t.Errorf("Expected fingerprint %s, got %s", originalData.Fingerprint, loadedData.Fingerprint)
		}

		if loadedData.SimilarityThreshold != originalData.SimilarityThreshold {
			t.Errorf("Expected threshold %d, got %d", originalData.SimilarityThreshold, loadedData.SimilarityThreshold)
		}

		// Hardware hash should be updated to current hardware
		if loadedData.HardwareHash == nil {
			t.Error("Expected non-nil HardwareHash")
		}
	})

	t.Run("should regenerate when machine ID changes", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		// Create initial fingerprint data with a specific machine ID
		initialData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "old-machine-id",
				HostnameInfo:  "test-host",
				IPAddressInfo: "192.168.1.100",
				MACAddrInfo:   "00:11:22:33:44:55",
			},
			SimilarityThreshold: 40,
		}

		// Save the initial fingerprint manually
		manager := NewManager(fingerprintPath)
		if err := manager.save(initialData); err != nil {
			t.Fatalf("Failed to save initial fingerprint: %v", err)
		}

		// Mock the current hardware with different machine ID
		// Note: In a real test, we'd need to mock sysinfo.GetHardwareInfo()
		// For now, we'll load and expect it to generate a new one since
		// the actual machine ID will be different from "old-machine-id"

		// Load with the manager - should detect machine ID change
		loadedData, isNew, err := manager.LoadOrGenerate()

		if err != nil {
			t.Fatalf("LoadOrGenerate failed: %v", err)
		}

		if !isNew {
			t.Error("Expected isNew to be true when machine ID changes")
		}

		if loadedData == nil {
			t.Fatal("Expected non-nil FingerprintData")
		}

		// The fingerprint should be different from the original
		if loadedData.Fingerprint == initialData.Fingerprint {
			t.Error("Expected new fingerprint to be generated when machine ID changes")
		}

		// Verify it's still a valid UUID
		if _, err := uuid.Parse(loadedData.Fingerprint); err != nil {
			t.Errorf("New fingerprint is not a valid UUID: %v", err)
		}
	})

	t.Run("should regenerate when similarity below threshold", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		// Get current hardware info to use the same machine ID
		currentHardware, _ := sysinfo.GetHardwareInfo()

		// Create initial fingerprint data with significantly different hardware
		// but same machine ID (to pass the machine ID check)
		initialData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     currentHardware.MachineID, // Same machine ID
				HostnameInfo:  "old-host",
				IPAddressInfo: "10.0.0.1",
				MACAddrInfo:   "AA:BB:CC:DD:EE:FF",
				ProcessorHash: "old-processor",
				MemoryHash:    "old-memory",
				BiosHash:      "old-bios",
				SystemHash:    "old-system",
				DiskInfo:      "old-disk",
			},
			SimilarityThreshold: 40, // Threshold is 40%
		}

		// Save the initial fingerprint
		manager := NewManager(fingerprintPath)
		if err := manager.save(initialData); err != nil {
			t.Fatalf("Failed to save initial fingerprint: %v", err)
		}

		// Load with the manager - should detect low similarity
		// The actual hardware will be very different from our mock data above
		// This should result in similarity < 40%
		loadedData, isNew, err := manager.LoadOrGenerate()

		if err != nil {
			t.Fatalf("LoadOrGenerate failed: %v", err)
		}

		// Calculate actual similarity for debugging
		similarity := sysinfo.CalculateSimilarity(initialData.HardwareHash, currentHardware)

		// Since most fields are different, similarity should be below 40%
		if similarity >= 40 {
			t.Skipf("Similarity is %d%%, which is above threshold. This test requires significant hardware differences.", similarity)
		}

		if !isNew {
			t.Errorf("Expected isNew to be true when similarity (%d%%) is below threshold (40%%)", similarity)
		}

		if loadedData == nil {
			t.Fatal("Expected non-nil FingerprintData")
		}

		// The fingerprint should be different from the original
		if loadedData.Fingerprint == initialData.Fingerprint {
			t.Error("Expected new fingerprint to be generated when similarity is below threshold")
		}

		// Verify it's still a valid UUID
		if _, err := uuid.Parse(loadedData.Fingerprint); err != nil {
			t.Errorf("New fingerprint is not a valid UUID: %v", err)
		}
	})

	t.Run("should reuse fingerprint when similarity above threshold", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		// Get current hardware info
		currentHardware, _ := sysinfo.GetHardwareInfo()

		// Create initial fingerprint data with mostly similar hardware
		// Only change a few fields to keep similarity above 40%
		initialData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     currentHardware.MachineID, // Same
				HostnameInfo:  currentHardware.HostnameInfo, // Same
				IPAddressInfo: "192.168.1.99", // Different
				MACAddrInfo:   currentHardware.MACAddrInfo, // Same
				ProcessorHash: currentHardware.ProcessorHash, // Same
				MemoryHash:    currentHardware.MemoryHash, // Same
				BiosHash:      currentHardware.BiosHash, // Same
				SystemHash:    currentHardware.SystemHash, // Same
				DiskInfo:      currentHardware.DiskInfo, // Same
			},
			SimilarityThreshold: 40, // Threshold is 40%
		}

		// Save the initial fingerprint
		manager := NewManager(fingerprintPath)
		if err := manager.save(initialData); err != nil {
			t.Fatalf("Failed to save initial fingerprint: %v", err)
		}

		originalFingerprint := initialData.Fingerprint

		// Load with the manager - should reuse fingerprint due to high similarity
		// 8 out of 9 fields are the same = 88% similarity
		loadedData, isNew, err := manager.LoadOrGenerate()

		if err != nil {
			t.Fatalf("LoadOrGenerate failed: %v", err)
		}

		// Calculate actual similarity for verification
		similarity := sysinfo.CalculateSimilarity(initialData.HardwareHash, currentHardware)

		// If machine ID and IP both match (special case), or similarity is high
		if similarity < 40 {
			t.Skipf("Similarity is %d%%, which is below threshold. This test requires high hardware similarity.", similarity)
		}

		if isNew {
			t.Errorf("Expected isNew to be false when similarity (%d%%) is above threshold (40%%)", similarity)
		}

		if loadedData == nil {
			t.Fatal("Expected non-nil FingerprintData")
		}

		// The fingerprint should remain the same
		if loadedData.Fingerprint != originalFingerprint {
			t.Errorf("Expected to reuse fingerprint %s, but got %s", originalFingerprint, loadedData.Fingerprint)
		}

		// Hardware hash should be updated to current hardware
		if loadedData.HardwareHash == nil {
			t.Error("Expected non-nil HardwareHash")
		}

		// Verify the hardware was updated even though fingerprint was reused
		if loadedData.HardwareHash.IPAddressInfo == "192.168.1.99" {
			t.Error("Hardware hash should be updated to current hardware")
		}
	})

	t.Run("should update hardware info even when reusing fingerprint", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		// Get current hardware info
		currentHardware, _ := sysinfo.GetHardwareInfo()

		// Create initial fingerprint with slightly outdated hardware info
		oldIPAddress := "10.0.0.100"
		oldHostname := "old-hostname"

		initialData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     currentHardware.MachineID, // Keep same to avoid regeneration
				HostnameInfo:  oldHostname,               // Different
				IPAddressInfo: oldIPAddress,              // Different
				MACAddrInfo:   currentHardware.MACAddrInfo,
				ProcessorHash: currentHardware.ProcessorHash,
				MemoryHash:    currentHardware.MemoryHash,
				BiosHash:      currentHardware.BiosHash,
				SystemHash:    currentHardware.SystemHash,
				DiskInfo:      currentHardware.DiskInfo,
			},
			SimilarityThreshold: 40,
		}

		// Save the initial fingerprint
		manager := NewManager(fingerprintPath)
		if err := manager.save(initialData); err != nil {
			t.Fatalf("Failed to save initial fingerprint: %v", err)
		}

		originalFingerprint := initialData.Fingerprint

		// Load with the manager
		loadedData, isNew, err := manager.LoadOrGenerate()

		if err != nil {
			t.Fatalf("LoadOrGenerate failed: %v", err)
		}

		// Calculate similarity to ensure we're in the reuse case
		similarity := sysinfo.CalculateSimilarity(initialData.HardwareHash, currentHardware)

		if similarity < 40 {
			t.Skipf("Similarity is %d%%, test requires similarity above threshold", similarity)
		}

		if isNew {
			t.Error("Expected fingerprint to be reused, not regenerated")
		}

		// Fingerprint should be the same
		if loadedData.Fingerprint != originalFingerprint {
			t.Errorf("Expected fingerprint %s, got %s", originalFingerprint, loadedData.Fingerprint)
		}

		// Hardware info should be updated to current values
		if loadedData.HardwareHash.IPAddressInfo == oldIPAddress {
			t.Errorf("IP address should be updated from %s to current value %s",
				oldIPAddress, currentHardware.IPAddressInfo)
		}

		if loadedData.HardwareHash.HostnameInfo == oldHostname {
			t.Errorf("Hostname should be updated from %s to current value %s",
				oldHostname, currentHardware.HostnameInfo)
		}

		// Verify the file was actually saved with updated hardware
		reloadedData, err := manager.load()
		if err != nil {
			t.Fatalf("Failed to reload fingerprint: %v", err)
		}

		if reloadedData.HardwareHash.IPAddressInfo == oldIPAddress {
			t.Error("Updated hardware info was not persisted to file")
		}
	})

	t.Run("should handle file read errors", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		// Create a file with invalid JSON
		if err := os.WriteFile(fingerprintPath, []byte("invalid json content"), 0600); err != nil {
			t.Fatalf("Failed to create invalid file: %v", err)
		}

		manager := NewManager(fingerprintPath)

		// Should generate new fingerprint when file is corrupted
		data, isNew, err := manager.LoadOrGenerate()

		if err != nil {
			t.Fatalf("LoadOrGenerate should handle corrupted file gracefully: %v", err)
		}

		if !isNew {
			t.Error("Expected isNew to be true when file is corrupted")
		}

		if data == nil {
			t.Fatal("Expected non-nil FingerprintData")
		}

		if data.Fingerprint == "" {
			t.Error("Expected non-empty fingerprint")
		}

		// Verify it's a valid UUID
		if _, err := uuid.Parse(data.Fingerprint); err != nil {
			t.Errorf("Fingerprint is not a valid UUID: %v", err)
		}

		// Verify the corrupted file was replaced with valid JSON
		reloadedData, err := manager.load()
		if err != nil {
			t.Errorf("Failed to load newly generated fingerprint: %v", err)
		}

		if reloadedData.Fingerprint != data.Fingerprint {
			t.Error("New fingerprint was not properly saved")
		}
	})

	t.Run("should handle file write errors", func(t *testing.T) {
		// Skip this test if running as root (root can write to read-only dirs)
		if os.Geteuid() == 0 {
			t.Skip("Skipping test when running as root")
		}

		tempDir := t.TempDir()
		readonlyDir := filepath.Join(tempDir, "readonly")

		// Create the directory first
		if err := os.MkdirAll(readonlyDir, 0755); err != nil {
			t.Fatalf("Failed to create directory: %v", err)
		}

		// Make it read-only
		if err := os.Chmod(readonlyDir, 0444); err != nil {
			t.Fatalf("Failed to set read-only permissions: %v", err)
		}
		defer os.Chmod(readonlyDir, 0755) // Restore for cleanup

		fingerprintPath := filepath.Join(readonlyDir, "fingerprint.json")
		manager := NewManager(fingerprintPath)

		// Attempt to generate fingerprint - should fail due to write error
		data, isNew, err := manager.LoadOrGenerate()

		// Should return an error when unable to write
		if err == nil {
			t.Error("Expected error when unable to write file")
		}

		if data != nil {
			t.Error("Expected nil data when write fails")
		}

		if isNew {
			t.Error("Expected isNew to be false when write fails")
		}
	})
}

func TestSave(t *testing.T) {
	t.Run("should save fingerprint data as JSON", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		testData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "test-machine-id",
				HostnameInfo:  "test-hostname",
				IPAddressInfo: "192.168.1.100",
				MACAddrInfo:   "00:11:22:33:44:55",
				ProcessorHash: "processor-hash",
				MemoryHash:    "memory-hash",
				BiosHash:      "bios-hash",
				SystemHash:    "system-hash",
				DiskInfo:      "disk-info",
			},
			SimilarityThreshold: 40,
		}

		// Save the data
		err := manager.save(testData)

		if err != nil {
			t.Fatalf("Failed to save fingerprint data: %v", err)
		}

		// Verify file was created
		if _, err := os.Stat(fingerprintPath); os.IsNotExist(err) {
			t.Error("Fingerprint file was not created")
		}

		// Read the file and verify it's valid JSON
		fileContent, err := os.ReadFile(fingerprintPath)
		if err != nil {
			t.Fatalf("Failed to read saved file: %v", err)
		}

		// Verify it's valid JSON
		var loadedData FingerprintData
		if err := json.Unmarshal(fileContent, &loadedData); err != nil {
			t.Errorf("Saved file is not valid JSON: %v", err)
		}

		// Verify the content matches
		if loadedData.Fingerprint != testData.Fingerprint {
			t.Errorf("Expected fingerprint %s, got %s", testData.Fingerprint, loadedData.Fingerprint)
		}

		if loadedData.SimilarityThreshold != testData.SimilarityThreshold {
			t.Errorf("Expected threshold %d, got %d", testData.SimilarityThreshold, loadedData.SimilarityThreshold)
		}

		if loadedData.HardwareHash.MachineID != testData.HardwareHash.MachineID {
			t.Errorf("Expected machine ID %s, got %s",
				testData.HardwareHash.MachineID, loadedData.HardwareHash.MachineID)
		}

		// Verify JSON is indented (formatted)
		if !strings.Contains(string(fileContent), "\n  ") {
			t.Error("Expected JSON to be indented")
		}
	})

	t.Run("should create directory if it doesn't exist", func(t *testing.T) {
		tempDir := t.TempDir()
		// Create a nested path that doesn't exist
		nestedPath := filepath.Join(tempDir, "level1", "level2", "level3", "fingerprint.json")

		manager := NewManager(nestedPath)

		testData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "test-machine-id",
				HostnameInfo:  "test-hostname",
				IPAddressInfo: "192.168.1.100",
				MACAddrInfo:   "00:11:22:33:44:55",
			},
			SimilarityThreshold: 40,
		}

		// Verify directories don't exist initially
		parentDir := filepath.Dir(nestedPath)
		if _, err := os.Stat(parentDir); !os.IsNotExist(err) {
			t.Fatal("Directory should not exist initially")
		}

		// Save should create all necessary directories
		err := manager.save(testData)

		if err != nil {
			t.Fatalf("Failed to save fingerprint data: %v", err)
		}

		// Verify all directories were created
		if _, err := os.Stat(parentDir); os.IsNotExist(err) {
			t.Error("Parent directories were not created")
		}

		// Verify the file was created
		if _, err := os.Stat(nestedPath); os.IsNotExist(err) {
			t.Error("Fingerprint file was not created")
		}

		// Verify directory has correct permissions (0700)
		dirInfo, err := os.Stat(parentDir)
		if err != nil {
			t.Fatalf("Failed to stat directory: %v", err)
		}

		expectedPerm := os.FileMode(0700)
		actualPerm := dirInfo.Mode().Perm()
		if actualPerm != expectedPerm {
			t.Errorf("Expected directory permissions %o, got %o", expectedPerm, actualPerm)
		}
	})

	t.Run("should save file with 0600 permissions", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		testData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "test-machine-id",
				HostnameInfo:  "test-hostname",
				IPAddressInfo: "192.168.1.100",
			},
			SimilarityThreshold: 40,
		}

		// Save the file
		err := manager.save(testData)

		if err != nil {
			t.Fatalf("Failed to save fingerprint data: %v", err)
		}

		// Check file permissions
		fileInfo, err := os.Stat(fingerprintPath)
		if err != nil {
			t.Fatalf("Failed to stat file: %v", err)
		}

		expectedPerm := os.FileMode(0600)
		actualPerm := fileInfo.Mode().Perm()
		if actualPerm != expectedPerm {
			t.Errorf("Expected file permissions %o, got %o", expectedPerm, actualPerm)
		}

		// Verify that only the owner can read/write
		if actualPerm&0077 != 0 {
			t.Error("File should not be readable or writable by group or others")
		}
	})

	t.Run("should overwrite existing file", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Save initial data
		initialData := &FingerprintData{
			Fingerprint: "initial-fingerprint",
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "initial-machine-id",
				HostnameInfo:  "initial-hostname",
				IPAddressInfo: "192.168.1.1",
			},
			SimilarityThreshold: 30,
		}

		if err := manager.save(initialData); err != nil {
			t.Fatalf("Failed to save initial data: %v", err)
		}

		// Save new data to overwrite
		newData := &FingerprintData{
			Fingerprint: "new-fingerprint",
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "new-machine-id",
				HostnameInfo:  "new-hostname",
				IPAddressInfo: "192.168.1.2",
			},
			SimilarityThreshold: 50,
		}

		if err := manager.save(newData); err != nil {
			t.Fatalf("Failed to overwrite file: %v", err)
		}

		// Read the file and verify it contains new data
		fileContent, err := os.ReadFile(fingerprintPath)
		if err != nil {
			t.Fatalf("Failed to read file: %v", err)
		}

		var loadedData FingerprintData
		if err := json.Unmarshal(fileContent, &loadedData); err != nil {
			t.Fatalf("Failed to unmarshal JSON: %v", err)
		}

		// Verify new data replaced old data
		if loadedData.Fingerprint != newData.Fingerprint {
			t.Errorf("Expected fingerprint %s, got %s", newData.Fingerprint, loadedData.Fingerprint)
		}

		if loadedData.HardwareHash.MachineID != newData.HardwareHash.MachineID {
			t.Errorf("Expected machine ID %s, got %s",
				newData.HardwareHash.MachineID, loadedData.HardwareHash.MachineID)
		}

		if loadedData.SimilarityThreshold != newData.SimilarityThreshold {
			t.Errorf("Expected threshold %d, got %d",
				newData.SimilarityThreshold, loadedData.SimilarityThreshold)
		}

		// Verify old data is completely gone
		if strings.Contains(string(fileContent), "initial-fingerprint") {
			t.Error("File still contains old fingerprint data")
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("should load valid fingerprint file", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Create test data
		expectedData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "test-machine-id",
				HostnameInfo:  "test-hostname",
				IPAddressInfo: "192.168.1.100",
				MACAddrInfo:   "00:11:22:33:44:55",
				ProcessorHash: "processor-hash",
				MemoryHash:    "memory-hash",
				BiosHash:      "bios-hash",
				SystemHash:    "system-hash",
				DiskInfo:      "disk-info",
			},
			SimilarityThreshold: 45,
		}

		// Save the data first
		if err := manager.save(expectedData); err != nil {
			t.Fatalf("Failed to save test data: %v", err)
		}

		// Load the data
		loadedData, err := manager.load()

		if err != nil {
			t.Fatalf("Failed to load fingerprint: %v", err)
		}

		if loadedData == nil {
			t.Fatal("Expected non-nil loaded data")
		}

		// Verify all fields match
		if loadedData.Fingerprint != expectedData.Fingerprint {
			t.Errorf("Expected fingerprint %s, got %s",
				expectedData.Fingerprint, loadedData.Fingerprint)
		}

		if loadedData.SimilarityThreshold != expectedData.SimilarityThreshold {
			t.Errorf("Expected threshold %d, got %d",
				expectedData.SimilarityThreshold, loadedData.SimilarityThreshold)
		}

		// Verify hardware info
		if loadedData.HardwareHash == nil {
			t.Fatal("Expected non-nil hardware hash")
		}

		if loadedData.HardwareHash.MachineID != expectedData.HardwareHash.MachineID {
			t.Errorf("Expected machine ID %s, got %s",
				expectedData.HardwareHash.MachineID, loadedData.HardwareHash.MachineID)
		}

		if loadedData.HardwareHash.HostnameInfo != expectedData.HardwareHash.HostnameInfo {
			t.Errorf("Expected hostname %s, got %s",
				expectedData.HardwareHash.HostnameInfo, loadedData.HardwareHash.HostnameInfo)
		}

		if loadedData.HardwareHash.IPAddressInfo != expectedData.HardwareHash.IPAddressInfo {
			t.Errorf("Expected IP %s, got %s",
				expectedData.HardwareHash.IPAddressInfo, loadedData.HardwareHash.IPAddressInfo)
		}

		if loadedData.HardwareHash.MACAddrInfo != expectedData.HardwareHash.MACAddrInfo {
			t.Errorf("Expected MAC %s, got %s",
				expectedData.HardwareHash.MACAddrInfo, loadedData.HardwareHash.MACAddrInfo)
		}
	})

	t.Run("should return error for non-existent file", func(t *testing.T) {
		tempDir := t.TempDir()
		nonExistentPath := filepath.Join(tempDir, "does-not-exist", "fingerprint.json")

		manager := NewManager(nonExistentPath)

		// Attempt to load non-existent file
		loadedData, err := manager.load()

		if err == nil {
			t.Error("Expected error when loading non-existent file")
		}

		if loadedData != nil {
			t.Error("Expected nil data when file doesn't exist")
		}

		// Verify it's specifically a "file not found" error
		if !os.IsNotExist(err) {
			t.Errorf("Expected os.IsNotExist error, got %v", err)
		}
	})

	t.Run("should return error for invalid JSON", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		// Create a file with invalid JSON
		invalidJSON := `{
			"fingerprint": "test-fingerprint",
			"hardwareHash": {
				"machine-id": "test-id",
				invalid json here
			}
		}`
		if err := os.WriteFile(fingerprintPath, []byte(invalidJSON), 0600); err != nil {
			t.Fatalf("Failed to create invalid JSON file: %v", err)
		}

		manager := NewManager(fingerprintPath)

		// Attempt to load invalid JSON
		loadedData, err := manager.load()

		if err == nil {
			t.Error("Expected error when loading invalid JSON")
		}

		if loadedData != nil {
			t.Error("Expected nil data when JSON is invalid")
		}

		// Verify error message mentions parsing
		if !strings.Contains(err.Error(), "failed to parse fingerprint file") {
			t.Errorf("Expected parse error, got: %v", err)
		}
	})

	t.Run("should preserve all fields when loading", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Create comprehensive test data with all fields populated
		originalData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "full-machine-id",
				HostnameInfo:  "full-hostname",
				IPAddressInfo: "10.20.30.40",
				MACAddrInfo:   "AA:BB:CC:DD:EE:FF",
				ProcessorHash: "proc-hash-12345",
				MemoryHash:    "mem-hash-67890",
				BiosHash:      "bios-hash-abcdef",
				SystemHash:    "sys-hash-ghijkl",
				DiskInfo:      "disk-uuid-mnopqr",
			},
			SimilarityThreshold: 75,
		}

		// Save the data
		if err := manager.save(originalData); err != nil {
			t.Fatalf("Failed to save data: %v", err)
		}

		// Load it back
		loadedData, err := manager.load()
		if err != nil {
			t.Fatalf("Failed to load data: %v", err)
		}

		// Verify every single field is preserved
		if loadedData.Fingerprint != originalData.Fingerprint {
			t.Errorf("Fingerprint not preserved: expected %s, got %s",
				originalData.Fingerprint, loadedData.Fingerprint)
		}

		if loadedData.SimilarityThreshold != originalData.SimilarityThreshold {
			t.Errorf("SimilarityThreshold not preserved: expected %d, got %d",
				originalData.SimilarityThreshold, loadedData.SimilarityThreshold)
		}

		// Check all hardware hash fields
		hw := loadedData.HardwareHash
		origHw := originalData.HardwareHash

		if hw.MachineID != origHw.MachineID {
			t.Errorf("MachineID not preserved: expected %s, got %s",
				origHw.MachineID, hw.MachineID)
		}

		if hw.HostnameInfo != origHw.HostnameInfo {
			t.Errorf("HostnameInfo not preserved: expected %s, got %s",
				origHw.HostnameInfo, hw.HostnameInfo)
		}

		if hw.IPAddressInfo != origHw.IPAddressInfo {
			t.Errorf("IPAddressInfo not preserved: expected %s, got %s",
				origHw.IPAddressInfo, hw.IPAddressInfo)
		}

		if hw.MACAddrInfo != origHw.MACAddrInfo {
			t.Errorf("MACAddrInfo not preserved: expected %s, got %s",
				origHw.MACAddrInfo, hw.MACAddrInfo)
		}

		if hw.ProcessorHash != origHw.ProcessorHash {
			t.Errorf("ProcessorHash not preserved: expected %s, got %s",
				origHw.ProcessorHash, hw.ProcessorHash)
		}

		if hw.MemoryHash != origHw.MemoryHash {
			t.Errorf("MemoryHash not preserved: expected %s, got %s",
				origHw.MemoryHash, hw.MemoryHash)
		}

		if hw.BiosHash != origHw.BiosHash {
			t.Errorf("BiosHash not preserved: expected %s, got %s",
				origHw.BiosHash, hw.BiosHash)
		}

		if hw.SystemHash != origHw.SystemHash {
			t.Errorf("SystemHash not preserved: expected %s, got %s",
				origHw.SystemHash, hw.SystemHash)
		}

		if hw.DiskInfo != origHw.DiskInfo {
			t.Errorf("DiskInfo not preserved: expected %s, got %s",
				origHw.DiskInfo, hw.DiskInfo)
		}
	})
}

func TestGetFingerprint(t *testing.T) {
	t.Run("should return fingerprint from loaded data", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Create and save test data
		expectedFingerprint := uuid.New().String()
		testData := &FingerprintData{
			Fingerprint: expectedFingerprint,
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "test-machine-id",
				HostnameInfo:  "test-hostname",
				IPAddressInfo: "192.168.1.100",
			},
			SimilarityThreshold: 40,
		}

		// Save the data first
		if err := manager.save(testData); err != nil {
			t.Fatalf("Failed to save test data: %v", err)
		}

		// Get the fingerprint
		fingerprint, err := manager.GetFingerprint()

		if err != nil {
			t.Fatalf("GetFingerprint failed: %v", err)
		}

		if fingerprint != expectedFingerprint {
			t.Errorf("Expected fingerprint %s, got %s", expectedFingerprint, fingerprint)
		}

		// Verify it's a valid UUID
		if _, err := uuid.Parse(fingerprint); err != nil {
			t.Errorf("Returned fingerprint is not a valid UUID: %v", err)
		}
	})

	t.Run("should return error if file doesn't exist", func(t *testing.T) {
		tempDir := t.TempDir()
		nonExistentPath := filepath.Join(tempDir, "does-not-exist", "fingerprint.json")

		manager := NewManager(nonExistentPath)

		// Attempt to get fingerprint from non-existent file
		fingerprint, err := manager.GetFingerprint()

		if err == nil {
			t.Error("Expected error when file doesn't exist")
		}

		if fingerprint != "" {
			t.Errorf("Expected empty fingerprint when file doesn't exist, got %s", fingerprint)
		}

		// Verify it's a file not found error
		if !os.IsNotExist(err) {
			t.Errorf("Expected os.IsNotExist error, got %v", err)
		}
	})

	t.Run("should return error if file is corrupted", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		// Create a corrupted JSON file
		corruptedJSON := `{
			"fingerprint": "test-fingerprint",
			this is not valid json
		}`
		if err := os.WriteFile(fingerprintPath, []byte(corruptedJSON), 0600); err != nil {
			t.Fatalf("Failed to create corrupted file: %v", err)
		}

		manager := NewManager(fingerprintPath)

		// Attempt to get fingerprint from corrupted file
		fingerprint, err := manager.GetFingerprint()

		if err == nil {
			t.Error("Expected error when file is corrupted")
		}

		if fingerprint != "" {
			t.Errorf("Expected empty fingerprint when file is corrupted, got %s", fingerprint)
		}

		// Verify error message indicates parsing failure
		if !strings.Contains(err.Error(), "failed to parse fingerprint file") {
			t.Errorf("Expected parse error, got: %v", err)
		}
	})
}

func TestFingerprintDataValidation(t *testing.T) {
	t.Run("should generate valid UUID for fingerprint", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Generate a new fingerprint
		data, isNew, err := manager.LoadOrGenerate()

		if err != nil {
			t.Fatalf("Failed to generate fingerprint: %v", err)
		}

		if !isNew {
			t.Error("Expected new fingerprint to be generated")
		}

		if data.Fingerprint == "" {
			t.Error("Fingerprint should not be empty")
		}

		// Validate it's a proper UUID v4
		parsedUUID, err := uuid.Parse(data.Fingerprint)
		if err != nil {
			t.Errorf("Fingerprint is not a valid UUID: %v", err)
		}

		// Verify it's a UUID version 4 (random)
		if parsedUUID.Version() != 4 {
			t.Errorf("Expected UUID version 4, got version %d", parsedUUID.Version())
		}

		// Verify UUID format (8-4-4-4-12)
		uuidRegex := `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
		matched, err := regexp.MatchString(uuidRegex, data.Fingerprint)
		if err != nil {
			t.Fatalf("Regex error: %v", err)
		}
		if !matched {
			t.Errorf("Fingerprint doesn't match UUID format: %s", data.Fingerprint)
		}
	})

	t.Run("should maintain similarity threshold", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Generate initial fingerprint
		initialData, _, err := manager.LoadOrGenerate()
		if err != nil {
			t.Fatalf("Failed to generate initial fingerprint: %v", err)
		}

		// Verify default threshold is 40
		if initialData.SimilarityThreshold != 40 {
			t.Errorf("Expected default threshold 40, got %d", initialData.SimilarityThreshold)
		}

		// Manually modify the threshold in the saved file
		modifiedData := &FingerprintData{
			Fingerprint:         initialData.Fingerprint,
			HardwareHash:        initialData.HardwareHash,
			SimilarityThreshold: 60, // Change threshold
		}

		if err := manager.save(modifiedData); err != nil {
			t.Fatalf("Failed to save modified data: %v", err)
		}

		// Load the data again - should maintain the modified threshold
		loadedData, isNew, err := manager.LoadOrGenerate()
		if err != nil {
			t.Fatalf("Failed to load fingerprint: %v", err)
		}

		if isNew {
			t.Error("Should not generate new fingerprint when loading existing one")
		}

		if loadedData.SimilarityThreshold != 60 {
			t.Errorf("Expected threshold to be maintained at 60, got %d", loadedData.SimilarityThreshold)
		}

		// Create a new manager with different threshold
		manager2 := NewManager(fingerprintPath)

		// When generating new fingerprint, it should use manager's threshold
		if err := os.Remove(fingerprintPath); err != nil {
			t.Fatalf("Failed to remove file: %v", err)
		}

		newData, isNew, err := manager2.LoadOrGenerate()
		if err != nil {
			t.Fatalf("Failed to generate new fingerprint: %v", err)
		}

		if !isNew {
			t.Error("Expected new fingerprint to be generated")
		}

		// Should use the manager's default threshold (40)
		if newData.SimilarityThreshold != 40 {
			t.Errorf("Expected new fingerprint to use manager's threshold 40, got %d", newData.SimilarityThreshold)
		}
	})
}

func TestConcurrentAccess(t *testing.T) {
	t.Run("should handle concurrent reads safely", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Create test data
		testData := &FingerprintData{
			Fingerprint: uuid.New().String(),
			HardwareHash: &sysinfo.HardwareInfo{
				MachineID:     "test-machine-id",
				HostnameInfo:  "test-hostname",
				IPAddressInfo: "192.168.1.100",
			},
			SimilarityThreshold: 40,
		}

		// Save initial data
		if err := manager.save(testData); err != nil {
			t.Fatalf("Failed to save test data: %v", err)
		}

		// Number of concurrent readers
		numReaders := 100
		done := make(chan bool, numReaders)
		errors := make(chan error, numReaders)

		// Launch concurrent readers
		for i := 0; i < numReaders; i++ {
			go func(id int) {
				defer func() { done <- true }()

				// Perform multiple reads
				for j := 0; j < 10; j++ {
					data, err := manager.load()
					if err != nil {
						errors <- err
						return
					}

					// Verify data integrity
					if data.Fingerprint != testData.Fingerprint {
						errors <- fmt.Errorf("reader %d: fingerprint mismatch", id)
						return
					}

					if data.HardwareHash.MachineID != testData.HardwareHash.MachineID {
						errors <- fmt.Errorf("reader %d: machine ID mismatch", id)
						return
					}
				}
			}(i)
		}

		// Wait for all readers to complete
		for i := 0; i < numReaders; i++ {
			<-done
		}

		// Check for errors
		close(errors)
		for err := range errors {
			t.Errorf("Concurrent read error: %v", err)
		}
	})

	t.Run("should handle concurrent writes safely", func(t *testing.T) {
		tempDir := t.TempDir()
		fingerprintPath := filepath.Join(tempDir, "fingerprint.json")

		manager := NewManager(fingerprintPath)

		// Number of concurrent writers
		numWriters := 50
		done := make(chan bool, numWriters)
		errors := make(chan error, numWriters)

		// Launch concurrent writers
		for i := 0; i < numWriters; i++ {
			go func(id int) {
				defer func() { done <- true }()

				// Each writer creates its own unique data
				data := &FingerprintData{
					Fingerprint: fmt.Sprintf("fingerprint-%d", id),
					HardwareHash: &sysinfo.HardwareInfo{
						MachineID:     fmt.Sprintf("machine-%d", id),
						HostnameInfo:  fmt.Sprintf("host-%d", id),
						IPAddressInfo: fmt.Sprintf("192.168.1.%d", id),
					},
					SimilarityThreshold: 40 + id%10,
				}

				// Attempt to write
				if err := manager.save(data); err != nil {
					errors <- fmt.Errorf("writer %d: save failed: %v", id, err)
					return
				}

				// Immediately read back to verify write succeeded
				loadedData, err := manager.load()
				if err != nil {
					errors <- fmt.Errorf("writer %d: load failed: %v", id, err)
					return
				}

				// The loaded data should be valid (one of the written values)
				if loadedData.Fingerprint == "" {
					errors <- fmt.Errorf("writer %d: loaded empty fingerprint", id)
					return
				}

				if loadedData.HardwareHash == nil {
					errors <- fmt.Errorf("writer %d: loaded nil hardware hash", id)
					return
				}
			}(i)
		}

		// Wait for all writers to complete
		for i := 0; i < numWriters; i++ {
			<-done
		}

		// Check for errors
		close(errors)
		errorCount := 0
		for err := range errors {
			t.Errorf("Concurrent write error: %v", err)
			errorCount++
		}

		if errorCount > 0 {
			t.Errorf("Total concurrent write errors: %d", errorCount)
		}

		// Final verification - file should exist and be valid JSON
		finalData, err := manager.load()
		if err != nil {
			t.Fatalf("Failed to load final data: %v", err)
		}

		if finalData.Fingerprint == "" {
			t.Error("Final fingerprint is empty")
		}

		// Verify the file is valid JSON
		fileContent, err := os.ReadFile(fingerprintPath)
		if err != nil {
			t.Fatalf("Failed to read final file: %v", err)
		}

		var jsonData map[string]interface{}
		if err := json.Unmarshal(fileContent, &jsonData); err != nil {
			t.Errorf("Final file is not valid JSON: %v", err)
		}
	})
}

