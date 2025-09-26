package fingerprint

import (
	"encoding/json"
	"errors"
	"fmt"
	"hostlink/internal/sysinfo"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

type FingerprintData struct {
	Fingerprint         string                `json:"fingerprint"`
	HardwareHash        *sysinfo.HardwareInfo `json:"hardwareHash"`
	SimilarityThreshold int                   `json:"similarityThreshold"`
}

var fingerprintMutex sync.Mutex

type Manager struct {
	fingerprintPath string
	threshold       int
}

func NewManager(fingerprintPath string) *Manager {
	return &Manager{
		fingerprintPath: fingerprintPath,
		threshold:       40,
	}
}

func (m *Manager) LoadOrGenerate() (*FingerprintData, bool, error) {
	fingerprintMutex.Lock()
	defer fingerprintMutex.Unlock()

	currentHardware, err := sysinfo.GetHardwareInfo()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get hardware info: %w", err)
	}

	existingData, err := m.load()
	if err != nil {
		// Check if file doesn't exist or is corrupted JSON
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError

		if os.IsNotExist(err) ||
			errors.As(err, &syntaxErr) ||
			errors.As(err, &typeErr) {
			// Generate new fingerprint for missing or corrupted files
			newData := &FingerprintData{
				Fingerprint:         uuid.New().String(),
				HardwareHash:        currentHardware,
				SimilarityThreshold: m.threshold,
			}
			if err := m.save(newData); err != nil {
				return nil, false, err
			}
			return newData, true, nil
		}
		return nil, false, err
	}

	if existingData.HardwareHash.MachineID != currentHardware.MachineID {
		newData := &FingerprintData{
			Fingerprint:         uuid.New().String(),
			HardwareHash:        currentHardware,
			SimilarityThreshold: m.threshold,
		}
		if err := m.save(newData); err != nil {
			return nil, false, err
		}
		return newData, true, nil
	}

	similarity := sysinfo.CalculateSimilarity(existingData.HardwareHash, currentHardware)
	if similarity < existingData.SimilarityThreshold {
		newData := &FingerprintData{
			Fingerprint:         uuid.New().String(),
			HardwareHash:        currentHardware,
			SimilarityThreshold: m.threshold,
		}
		if err := m.save(newData); err != nil {
			return nil, false, err
		}
		return newData, true, nil
	}

	existingData.HardwareHash = currentHardware
	if err := m.save(existingData); err != nil {
		return nil, false, err
	}

	return existingData, false, nil
}

func (m *Manager) load() (*FingerprintData, error) {
	data, err := os.ReadFile(m.fingerprintPath)
	if err != nil {
		return nil, err
	}

	var fingerprint FingerprintData
	if err := json.Unmarshal(data, &fingerprint); err != nil {
		return nil, fmt.Errorf("failed to parse fingerprint file: %w", err)
	}

	return &fingerprint, nil
}

func (m *Manager) save(data *FingerprintData) error {
	dir := filepath.Dir(m.fingerprintPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal fingerprint: %w", err)
	}

	// Create a unique temporary file in the same directory
	tempFile, err := os.CreateTemp(dir, ".fingerprint-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempPath := tempFile.Name()

	// Write data to temp file
	if _, err := tempFile.Write(jsonData); err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		return fmt.Errorf("failed to write to temporary file: %w", err)
	}

	// Ensure data is flushed and file is closed
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Set correct permissions on temp file
	if err := os.Chmod(tempPath, 0600); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to set permissions on temporary file: %w", err)
	}

	// Atomically rename the temp file to the actual file
	if err := os.Rename(tempPath, m.fingerprintPath); err != nil {
		// Clean up temp file if rename fails
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename fingerprint file: %w", err)
	}

	return nil
}

func (m *Manager) GetFingerprint() (string, error) {
	data, err := m.load()
	if err != nil {
		return "", err
	}
	return data.Fingerprint, nil
}

