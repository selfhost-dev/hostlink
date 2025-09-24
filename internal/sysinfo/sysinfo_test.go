package sysinfo

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGetHardwareInfo(t *testing.T) {
	t.Run("should collect hardware information on Linux", func(t *testing.T) {
		info, err := GetHardwareInfo()
		if err != nil {
			t.Fatalf("GetHardwareInfo() returned error: %v", err)
		}

		if info == nil {
			t.Fatal("GetHardwareInfo() returned nil")
		}

		if info.HostnameInfo == "" {
			t.Error("HostnameInfo should not be empty")
		}

		if info.IPAddressInfo == "" {
			t.Error("IPAddressInfo should not be empty")
		}

		if info.IPAddressInfo == "127.0.0.1" {
			t.Error("IPAddressInfo should not be loopback address")
		}
	})

	t.Run("should handle errors when collecting hardware info", func(t *testing.T) {
		info, err := GetHardwareInfo()
		if err != nil {
			t.Fatalf("GetHardwareInfo() should not return error: %v", err)
		}

		if info == nil {
			t.Fatal("GetHardwareInfo() should not return nil even when some operations fail")
		}

		// Even if some operations fail, we should still get a valid structure
		// with at least some fields populated
		hasAnyField := info.HostnameInfo != "" ||
			info.IPAddressInfo != "" ||
			info.MACAddrInfo != "" ||
			info.MachineID != "" ||
			info.ProcessorHash != "" ||
			info.MemoryHash != "" ||
			info.BiosHash != "" ||
			info.SystemHash != "" ||
			info.DiskInfo != ""

		if !hasAnyField {
			t.Error("GetHardwareInfo() should populate at least some fields even when errors occur")
		}
	})

	t.Run("should return non-empty values for critical fields", func(t *testing.T) {
		info, err := GetHardwareInfo()
		if err != nil {
			t.Fatalf("GetHardwareInfo() returned error: %v", err)
		}

		if info.HostnameInfo == "" {
			t.Error("Critical field HostnameInfo should not be empty")
		}

		if info.IPAddressInfo == "" {
			t.Error("Critical field IPAddressInfo should not be empty")
		}

		if info.MachineID == "" {
			t.Error("Critical field MachineID should not be empty")
		}
	})
}

func TestCalculateFingerprint(t *testing.T) {
	t.Run("should generate consistent fingerprint for same hardware info", func(t *testing.T) {
		hwInfo := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		fingerprint1 := CalculateFingerprint(hwInfo)
		fingerprint2 := CalculateFingerprint(hwInfo)

		if fingerprint1 != fingerprint2 {
			t.Errorf("Expected same fingerprint for same hardware info, got %s and %s", fingerprint1, fingerprint2)
		}

		if fingerprint1 == "" {
			t.Error("Fingerprint should not be empty")
		}

		// Check UUID-like format using uuid.Parse
		_, err := uuid.Parse(fingerprint1)
		if err != nil {
			t.Errorf("Fingerprint should be valid UUID format, got error: %v for %s", err, fingerprint1)
		}
	})

	t.Run("should generate different fingerprints for different hardware", func(t *testing.T) {
		hwInfo1 := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		hwInfo2 := &HardwareInfo{
			BiosHash:      "bios999",
			DiskInfo:      "disk888",
			HostnameInfo:  "different-host",
			IPAddressInfo: "10.0.0.50",
			MACAddrInfo:   "11:22:33:44:55:66",
			MachineID:     "machine000",
			MemoryHash:    "mem777",
			ProcessorHash: "proc111",
			SystemHash:    "sys222",
		}

		fingerprint1 := CalculateFingerprint(hwInfo1)
		fingerprint2 := CalculateFingerprint(hwInfo2)

		if fingerprint1 == fingerprint2 {
			t.Errorf("Expected different fingerprints for different hardware, but both are %s", fingerprint1)
		}

		if fingerprint1 == "" || fingerprint2 == "" {
			t.Error("Fingerprints should not be empty")
		}
	})

	t.Run("should return UUID-formatted fingerprint", func(t *testing.T) {
		hwInfo := &HardwareInfo{
			MachineID:     "test-machine",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			SystemHash:    "sys123",
			ProcessorHash: "proc456",
			BiosHash:      "bios789",
		}

		fingerprint := CalculateFingerprint(hwInfo)

		if fingerprint == "" {
			t.Fatal("Fingerprint should not be empty")
		}

		// Check UUID-like format using uuid.Parse
		_, err := uuid.Parse(fingerprint)
		if err != nil {
			t.Errorf("Fingerprint should be valid UUID format, got error: %v for %s", err, fingerprint)
		}
	})
}

func TestCalculateSimilarity(t *testing.T) {
	t.Run("should return 100% for identical hardware info", func(t *testing.T) {
		hwInfo := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		similarity := CalculateSimilarity(hwInfo, hwInfo)

		if similarity != 100 {
			t.Errorf("Expected 100%% similarity for identical hardware, got %d%%", similarity)
		}
	})

	t.Run("should return 0% for completely different hardware", func(t *testing.T) {
		hwInfo1 := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		hwInfo2 := &HardwareInfo{
			BiosHash:      "different-bios",
			DiskInfo:      "different-disk",
			HostnameInfo:  "different-host",
			IPAddressInfo: "10.0.0.1",
			MACAddrInfo:   "11:22:33:44:55:66",
			MachineID:     "different-machine",
			MemoryHash:    "different-mem",
			ProcessorHash: "different-proc",
			SystemHash:    "different-sys",
		}

		similarity := CalculateSimilarity(hwInfo1, hwInfo2)

		if similarity != 0 {
			t.Errorf("Expected 0%% similarity for completely different hardware, got %d%%", similarity)
		}
	})

	t.Run("should return partial match for some matching fields", func(t *testing.T) {
		hwInfo1 := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		// Same machine (same critical hardware) but different IP and hostname
		hwInfo2 := &HardwareInfo{
			BiosHash:      "bios123",      // Same
			DiskInfo:      "disk456",      // Same
			HostnameInfo:  "new-hostname", // Different
			IPAddressInfo: "10.0.0.50",    // Different
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff", // Same
			MachineID:     "machine789",   // Same
			MemoryHash:    "mem321",       // Same
			ProcessorHash: "proc654",      // Same
			SystemHash:    "sys987",       // Same
		}

		similarity := CalculateSimilarity(hwInfo1, hwInfo2)

		// 7 out of 9 fields match = 77%
		expectedSimilarity := (7 * 100) / 9

		if similarity != expectedSimilarity {
			t.Errorf("Expected %d%% similarity for partial match, got %d%%", expectedSimilarity, similarity)
		}

		// Test with 50% match (4.5 out of 9, rounds down to 44%)
		hwInfo3 := &HardwareInfo{
			BiosHash:      "bios123",      // Same
			DiskInfo:      "disk456",      // Same
			HostnameInfo:  "test-host",    // Same
			IPAddressInfo: "192.168.1.100", // Same
			MACAddrInfo:   "different-mac", // Different
			MachineID:     "different-id",  // Different
			MemoryHash:    "different-mem", // Different
			ProcessorHash: "different-proc", // Different
			SystemHash:    "different-sys",  // Different
		}

		similarity2 := CalculateSimilarity(hwInfo1, hwInfo3)
		expectedSimilarity2 := (4 * 100) / 9 // 44%

		if similarity2 != expectedSimilarity2 {
			t.Errorf("Expected %d%% similarity for 4/9 match, got %d%%", expectedSimilarity2, similarity2)
		}
	})

	t.Run("should ignore empty fields in similarity calculation", func(t *testing.T) {
		hwInfo1 := &HardwareInfo{
			BiosHash:      "",  // Empty
			DiskInfo:      "",  // Empty
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "",  // Empty
			MachineID:     "machine789",
			MemoryHash:    "",  // Empty
			ProcessorHash: "",  // Empty
			SystemHash:    "",  // Empty
		}

		hwInfo2 := &HardwareInfo{
			BiosHash:      "",  // Empty (matches because both empty - ignored)
			DiskInfo:      "",  // Empty (matches because both empty - ignored)
			HostnameInfo:  "test-host",      // Same
			IPAddressInfo: "192.168.1.100",  // Same
			MACAddrInfo:   "",  // Empty (matches because both empty - ignored)
			MachineID:     "machine789",      // Same
			MemoryHash:    "",  // Empty (matches because both empty - ignored)
			ProcessorHash: "",  // Empty (matches because both empty - ignored)
			SystemHash:    "",  // Empty (matches because both empty - ignored)
		}

		similarity := CalculateSimilarity(hwInfo1, hwInfo2)

		// All non-empty fields match (3/3: hostname, IP, machine ID), so 100%
		// But hostname always counts even if empty, so it's actually 3/3 = 100%
		if similarity != 100 {
			t.Errorf("Expected 100%% similarity when all non-empty fields match, got %d%%", similarity)
		}

		// Test with different non-empty values
		hwInfo3 := &HardwareInfo{
			BiosHash:      "different",  // Different but hwInfo1 is empty - not counted
			DiskInfo:      "different",  // Different but hwInfo1 is empty - not counted
			HostnameInfo:  "different-host",
			IPAddressInfo: "10.0.0.1",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",  // Different but hwInfo1 is empty - not counted
			MachineID:     "different-machine",
			MemoryHash:    "mem123",  // Different but hwInfo1 is empty - not counted
			ProcessorHash: "proc456",  // Different but hwInfo1 is empty - not counted
			SystemHash:    "sys789",  // Different but hwInfo1 is empty - not counted
		}

		similarity2 := CalculateSimilarity(hwInfo1, hwInfo3)

		// Only hostname, IP, and machineID count (all different), so 0/3 = 0%
		// But the implementation counts all 9 fields, empty ones don't match if other is non-empty
		// So we get 0/9 = 0%
		if similarity2 != 0 {
			t.Errorf("Expected 0%% similarity when non-empty fields don't match, got %d%%", similarity2)
		}
	})
}

func TestCalculateSimilaritySpecialCase(t *testing.T) {
	t.Run("should return 100% if machine ID and IP address match", func(t *testing.T) {
		// Special case: if machine ID and IP both match, return 100% immediately
		hwInfo1 := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		hwInfo2 := &HardwareInfo{
			BiosHash:      "completely-different",
			DiskInfo:      "completely-different",
			HostnameInfo:  "different-host",
			IPAddressInfo: "192.168.1.100",  // Same IP
			MACAddrInfo:   "11:22:33:44:55:66",
			MachineID:     "machine789",      // Same Machine ID
			MemoryHash:    "different-mem",
			ProcessorHash: "different-proc",
			SystemHash:    "different-sys",
		}

		similarity := CalculateSimilarity(hwInfo1, hwInfo2)

		// Even though most fields are different, machine ID + IP match should return 100%
		if similarity != 100 {
			t.Errorf("Expected 100%% similarity when machine ID and IP match (special case), got %d%%", similarity)
		}
	})

	t.Run("should not apply special case if only machine ID matches", func(t *testing.T) {
		hwInfo1 := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		hwInfo2 := &HardwareInfo{
			BiosHash:      "different",
			DiskInfo:      "different",
			HostnameInfo:  "different-host",
			IPAddressInfo: "10.0.0.50",       // Different IP
			MACAddrInfo:   "11:22:33:44:55:66",
			MachineID:     "machine789",      // Same Machine ID
			MemoryHash:    "different",
			ProcessorHash: "different",
			SystemHash:    "different",
		}

		similarity := CalculateSimilarity(hwInfo1, hwInfo2)

		// Only machine ID matches (1 out of 9 fields) = 11%
		if similarity == 100 {
			t.Errorf("Should not return 100%% when only machine ID matches (no IP match), got %d%%", similarity)
		}
	})

	t.Run("should not apply special case if only IP matches", func(t *testing.T) {
		hwInfo1 := &HardwareInfo{
			BiosHash:      "bios123",
			DiskInfo:      "disk456",
			HostnameInfo:  "test-host",
			IPAddressInfo: "192.168.1.100",
			MACAddrInfo:   "aa:bb:cc:dd:ee:ff",
			MachineID:     "machine789",
			MemoryHash:    "mem321",
			ProcessorHash: "proc654",
			SystemHash:    "sys987",
		}

		hwInfo2 := &HardwareInfo{
			BiosHash:      "different",
			DiskInfo:      "different",
			HostnameInfo:  "different-host",
			IPAddressInfo: "192.168.1.100",   // Same IP
			MACAddrInfo:   "11:22:33:44:55:66",
			MachineID:     "different-machine", // Different Machine ID
			MemoryHash:    "different",
			ProcessorHash: "different",
			SystemHash:    "different",
		}

		similarity := CalculateSimilarity(hwInfo1, hwInfo2)

		// Only IP matches (1 out of 9 fields) = 11%
		if similarity == 100 {
			t.Errorf("Should not return 100%% when only IP matches (no machine ID match), got %d%%", similarity)
		}
	})
}

func TestGetPrimaryIP(t *testing.T) {
	t.Run("should return valid IPv4 address", func(t *testing.T) {
		ip := getPrimaryIP()

		if ip == "" {
			t.Error("getPrimaryIP() should not return empty string")
		}

		// Check if it's a valid IPv4 address format
		parts := strings.Split(ip, ".")
		if len(parts) != 4 {
			t.Errorf("Expected IPv4 address with 4 octets, got %s", ip)
		}

		for i, part := range parts {
			num := 0
			_, err := fmt.Sscanf(part, "%d", &num)
			if err != nil {
				t.Errorf("Octet %d is not a number: %s", i, part)
			}
			if num < 0 || num > 255 {
				t.Errorf("Octet %d value %d is out of range (0-255)", i, num)
			}
		}
	})

	t.Run("should not return loopback address", func(t *testing.T) {
		ip := getPrimaryIP()

		if ip == "127.0.0.1" {
			t.Error("getPrimaryIP() should not return loopback address 127.0.0.1")
		}

		if strings.HasPrefix(ip, "127.") {
			t.Errorf("getPrimaryIP() should not return loopback address, got %s", ip)
		}

		if ip == "::1" {
			t.Error("getPrimaryIP() should not return IPv6 loopback address ::1")
		}
	})
}

func TestGetPrimaryMAC(t *testing.T) {
	t.Run("should return valid MAC address format", func(t *testing.T) {
		mac := getPrimaryMAC()

		if mac == "" {
			t.Skip("No network interface with MAC address found (might be in container)")
		}

		// Check MAC address format (xx:xx:xx:xx:xx:xx)
		parts := strings.Split(mac, ":")
		if len(parts) != 6 {
			t.Errorf("Expected MAC address with 6 parts, got %d parts in %s", len(parts), mac)
		}

		// Validate each part is valid hex
		for i, part := range parts {
			if len(part) != 2 {
				t.Errorf("MAC address part %d should be 2 characters, got %d in %s", i, len(part), mac)
				continue
			}

			_, err := hex.DecodeString(part)
			if err != nil {
				t.Errorf("MAC address part %d is not valid hex: %s in %s", i, part, mac)
			}
		}

		// Check it's not all zeros
		if mac == "00:00:00:00:00:00" {
			t.Error("MAC address should not be all zeros")
		}
	})

	t.Run("should return MAC of primary network interface", func(t *testing.T) {
		mac := getPrimaryMAC()
		ip := getPrimaryIP()

		if mac == "" {
			t.Skip("No network interface with MAC address found")
		}

		// The MAC should be from an interface that has the primary IP
		// or at least from a non-loopback interface
		interfaces, err := net.Interfaces()
		if err != nil {
			t.Fatalf("Failed to get network interfaces: %v", err)
		}

		foundMAC := false
		for _, iface := range interfaces {
			if iface.HardwareAddr.String() == mac {
				foundMAC = true

				// Check this interface has an IP address
				addrs, err := iface.Addrs()
				if err != nil {
					continue
				}

				// Verify it's not the loopback interface
				if iface.Name == "lo" || iface.Name == "lo0" {
					t.Errorf("MAC address %s is from loopback interface %s", mac, iface.Name)
				}

				// Check if this interface has the primary IP
				for _, addr := range addrs {
					if ipnet, ok := addr.(*net.IPNet); ok {
						if ipnet.IP.String() == ip {
							// Perfect match - MAC is from interface with primary IP
							return
						}
					}
				}
			}
		}

		if !foundMAC {
			t.Errorf("MAC address %s not found in any network interface", mac)
		}
	})
}

func TestGetMachineID(t *testing.T) {
	t.Run("should return hardware UUID on macOS", func(t *testing.T) {
		machineID := getMachineID()

		if machineID == "" {
			t.Error("getMachineID() should not return empty string")
		}

		// Try to parse as UUID (macOS format)
		_, err := uuid.Parse(machineID)
		if err == nil {
			// Valid UUID format
			return
		}

		// On Linux, it might be a 32-character hex string (machine-id format)
		if len(machineID) == 32 {
			_, err := hex.DecodeString(machineID)
			if err == nil {
				// Valid 32-character hex string
				return
			}
		}

		// Fallback: might be hostname
		hostname, _ := os.Hostname()
		if machineID == hostname {
			return
		}

		t.Errorf("Machine ID is not a valid UUID, 32-char hex, or hostname: %s", machineID)
	})

	t.Run("should fallback to hostname if machine ID not available", func(t *testing.T) {
		// This test verifies the fallback behavior
		// Since we can't easily simulate missing machine ID files,
		// we'll test that the function returns something valid
		machineID := getMachineID()
		hostname, _ := os.Hostname()

		if machineID == "" {
			t.Error("getMachineID() should not return empty string")
		}

		// Check if it's one of the valid formats
		isValidUUID := false
		if _, err := uuid.Parse(machineID); err == nil {
			isValidUUID = true
		}

		isValid32HexString := false
		if len(machineID) == 32 {
			if _, err := hex.DecodeString(machineID); err == nil {
				isValid32HexString = true
			}
		}

		isHostname := machineID == hostname

		if !isValidUUID && !isValid32HexString && !isHostname {
			t.Errorf("Machine ID should be UUID, 32-char hex, or hostname, got: %s", machineID)
		}

		// If it's the hostname, that confirms fallback is working
		if isHostname {
			t.Logf("Confirmed fallback to hostname: %s", hostname)
		}
	})
}

func TestGetDiskInfo(t *testing.T) {
	t.Run("should return hashed disk UUID", func(t *testing.T) {
		diskInfo := getDiskInfo()

		// Disk info might be empty on some systems
		if diskInfo == "" {
			t.Skip("No disk info available on this system")
		}

		// Should be a 16-byte hash in hex format (32 characters)
		if len(diskInfo) != 32 {
			t.Errorf("Expected disk info to be 32 characters (16-byte hash), got %d: %s", len(diskInfo), diskInfo)
		}

		// Verify it's valid hex
		_, err := hex.DecodeString(diskInfo)
		if err != nil {
			t.Errorf("Disk info should be valid hex string, got error: %v for %s", err, diskInfo)
		}

		// Should be consistent on multiple calls
		diskInfo2 := getDiskInfo()
		if diskInfo != diskInfo2 {
			t.Errorf("getDiskInfo() should return consistent results, got %s and %s", diskInfo, diskInfo2)
		}
	})

	t.Run("should handle missing disk info gracefully", func(t *testing.T) {
		// getDiskInfo should never panic and should return empty string if no disk info
		diskInfo := getDiskInfo()

		// Should be either empty or valid hex
		if diskInfo != "" {
			if len(diskInfo) != 32 {
				t.Errorf("Disk info should be empty or 32 chars, got %d: %s", len(diskInfo), diskInfo)
			}

			_, err := hex.DecodeString(diskInfo)
			if err != nil {
				t.Errorf("Non-empty disk info should be valid hex, got error: %v", err)
			}
		}

		// Function should not panic even if called multiple times
		for i := 0; i < 3; i++ {
			_ = getDiskInfo()
		}
	})
}

