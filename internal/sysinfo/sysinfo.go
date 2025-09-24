// Package sysinfo provides system information gathering utilities
package sysinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HardwareInfo contains all hardware information for fingerprinting
type HardwareInfo struct {
	BiosHash      string `json:"bios-hash"`
	DiskInfo      string `json:"disk-info"`
	HostnameInfo  string `json:"hostname-info"`
	IPAddressInfo string `json:"ipaddress-info"`
	MACAddrInfo   string `json:"macaddr-info"`
	MachineID     string `json:"machine-id"`
	MemoryHash    string `json:"memory-hash"`
	ProcessorHash string `json:"processor-hash"`
	SystemHash    string `json:"system-hash"`
}

// GetHardwareInfo collects all hardware information
func GetHardwareInfo() (*HardwareInfo, error) {
	info := &HardwareInfo{}

	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	info.HostnameInfo = hostname

	// Get primary IP address
	info.IPAddressInfo = getPrimaryIP()

	// Get MAC address
	info.MACAddrInfo = getPrimaryMAC()

	// Get machine ID (Linux specific)
	info.MachineID = getMachineID()

	// Get hardware hashes using dmidecode (requires root on Linux)
	info.ProcessorHash = getDmidecodeHash("processor")
	info.MemoryHash = getDmidecodeHash("memory")
	info.BiosHash = getDmidecodeHash("bios")
	info.SystemHash = getDmidecodeHash("system")

	// Get disk information
	info.DiskInfo = getDiskInfo()

	return info, nil
}

// CalculateFingerprint generates a UUID-like fingerprint from hardware info
func CalculateFingerprint(info *HardwareInfo) string {
	// Combine key hardware identifiers
	combined := fmt.Sprintf("%s-%s-%s-%s-%s",
		info.MachineID,
		info.MACAddrInfo,
		info.SystemHash,
		info.ProcessorHash,
		info.BiosHash,
	)

	// Generate SHA256 hash
	hash := sha256.Sum256([]byte(combined))
	hashStr := hex.EncodeToString(hash[:])

	// Format as UUID-like string (8-4-4-4-12)
	if len(hashStr) >= 32 {
		return fmt.Sprintf("%s-%s-%s-%s-%s",
			hashStr[0:8],
			hashStr[8:12],
			hashStr[12:16],
			hashStr[16:20],
			hashStr[20:32],
		)
	}

	return hashStr
}

// CalculateSimilarity calculates the similarity percentage between two hardware infos
func CalculateSimilarity(info1, info2 *HardwareInfo) int {
	// Special case: if machine ID and IP address both match, return 100%
	if info1.MachineID != "" && info1.MachineID == info2.MachineID &&
		info1.IPAddressInfo != "" && info1.IPAddressInfo == info2.IPAddressInfo {
		return 100
	}

	totalFields := 9
	matchingFields := 0

	// Check each field - count as match if both equal (including both empty)
	if info1.BiosHash == info2.BiosHash {
		matchingFields++
	}
	if info1.DiskInfo == info2.DiskInfo {
		matchingFields++
	}
	if info1.HostnameInfo == info2.HostnameInfo {
		matchingFields++
	}
	if info1.IPAddressInfo == info2.IPAddressInfo {
		matchingFields++
	}
	if info1.MACAddrInfo == info2.MACAddrInfo {
		matchingFields++
	}
	if info1.MachineID == info2.MachineID {
		matchingFields++
	}
	if info1.MemoryHash == info2.MemoryHash {
		matchingFields++
	}
	if info1.ProcessorHash == info2.ProcessorHash {
		matchingFields++
	}
	if info1.SystemHash == info2.SystemHash {
		matchingFields++
	}

	return (matchingFields * 100) / totalFields
}

// getPrimaryIP returns the primary non-loopback IP address
func getPrimaryIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return getFirstNonLoopbackIP()
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// getFirstNonLoopbackIP fallback to get first non-loopback IP
func getFirstNonLoopbackIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

// getPrimaryMAC returns the MAC address of the primary network interface
func getPrimaryMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	// Try to find the interface with the primary IP
	primaryIP := getPrimaryIP()
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.String() == primaryIP {
					return iface.HardwareAddr.String()
				}
			}
		}
	}

	// Fallback: return first non-empty MAC
	for _, iface := range interfaces {
		if iface.HardwareAddr.String() != "" && iface.Name != "lo" {
			return iface.HardwareAddr.String()
		}
	}

	return ""
}

// getMachineID returns the machine ID (Linux specific)
func getMachineID() string {
	// Try Linux machine-id first
	if data, err := os.ReadFile("/etc/machine-id"); err == nil {
		return strings.TrimSpace(string(data))
	}

	// Try systemd machine-id
	if data, err := os.ReadFile("/var/lib/dbus/machine-id"); err == nil {
		return strings.TrimSpace(string(data))
	}

	// macOS doesn't have machine-id, use hardware UUID
	if output, err := exec.Command("system_profiler", "SPHardwareDataType").Output(); err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "Hardware UUID:") {
				parts := strings.Split(line, ":")
				if len(parts) >= 2 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}

	// Fallback to hostname
	hostname, _ := os.Hostname()
	return hostname
}

// getDmidecodeHash returns a hash of dmidecode output for a given type
func getDmidecodeHash(dmidecodeType string) string {
	// Try dmidecode (Linux, requires root)
	cmd := exec.Command("dmidecode", "-t", dmidecodeType)
	output, err := cmd.Output()
	if err != nil {
		// On macOS or if dmidecode fails, use system_profiler
		return getSystemProfilerHash(dmidecodeType)
	}

	// Hash the output
	hash := sha256.Sum256(output)
	return hex.EncodeToString(hash[:16]) // Use first 16 bytes for shorter hash
}

// getSystemProfilerHash macOS alternative to dmidecode
func getSystemProfilerHash(infoType string) string {
	var spType string
	switch infoType {
	case "processor":
		spType = "SPHardwareDataType"
	case "memory":
		spType = "SPMemoryDataType"
	case "system":
		spType = "SPSoftwareDataType"
	case "bios":
		spType = "SPHardwareDataType" // macOS doesn't have BIOS info
	default:
		return ""
	}

	cmd := exec.Command("system_profiler", spType)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	hash := sha256.Sum256(output)
	return hex.EncodeToString(hash[:16])
}

// getDiskInfo returns disk UUID information
func getDiskInfo() string {
	// Linux: try to get disk UUIDs
	if entries, err := os.ReadDir("/dev/disk/by-uuid"); err == nil {
		var uuids []string
		for _, entry := range entries {
			if !entry.IsDir() {
				// Resolve symlink to get actual device
				if target, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-uuid", entry.Name())); err == nil {
					uuids = append(uuids, fmt.Sprintf("%s:%s", entry.Name(), filepath.Base(target)))
				}
			}
		}
		if len(uuids) > 0 {
			// Hash the combined UUIDs for consistency
			combined := strings.Join(uuids, ";")
			hash := sha256.Sum256([]byte(combined))
			return hex.EncodeToString(hash[:16])
		}
	}

	// macOS: use diskutil
	if output, err := exec.Command("diskutil", "info", "/").Output(); err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "Volume UUID:") || strings.Contains(line, "Disk / Partition UUID:") {
				parts := strings.Fields(line)
				if len(parts) > 0 {
					uuid := parts[len(parts)-1]
					hash := sha256.Sum256([]byte(uuid))
					return hex.EncodeToString(hash[:16])
				}
			}
		}
	}

	return ""
}