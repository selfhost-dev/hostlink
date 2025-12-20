package storagemetrics

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

var supportedFilesystems = map[string]bool{
	"xfs":      true,
	"btrfs":    true,
	"ext":      true,
	"ext2":     true,
	"ext3":     true,
	"ext4":     true,
	"hfs":      true,
	"vxfs":     true,
	"reiserfs": true,
}

type mountInfoReader struct {
	readFile func(string) ([]byte, error)
}

func NewMountInfoReader() MountInfoReader {
	return &mountInfoReader{readFile: os.ReadFile}
}

func (r *mountInfoReader) ReadMounts() ([]MountInfo, error) {
	return readMountsWithReader(r.readFile)
}

func readMountsWithReader(readFile func(string) ([]byte, error)) ([]MountInfo, error) {
	content, err := readFile("/proc/self/mountinfo")
	if err == nil {
		return ParseMountInfo(string(content))
	}

	content, err = readFile("/proc/self/mounts")
	if err == nil {
		return ParseMounts(string(content))
	}

	content, err = readFile("/etc/mtab")
	if err == nil {
		return ParseMounts(string(content))
	}

	return nil, err
}

func ParseMountInfo(content string) ([]MountInfo, error) {
	var mounts []MountInfo

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		mount, ok := parseMountInfoLine(line)
		if !ok {
			continue
		}

		if !supportedFilesystems[mount.FilesystemType] {
			continue
		}

		mounts = append(mounts, mount)
	}

	return mounts, nil
}

func ParseMounts(content string) ([]MountInfo, error) {
	var mounts []MountInfo

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		mount, ok := parseMountsLine(line)
		if !ok {
			continue
		}

		if !supportedFilesystems[mount.FilesystemType] {
			continue
		}

		mounts = append(mounts, mount)
	}

	return mounts, nil
}

func parseMountsLine(line string) (MountInfo, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return MountInfo{}, false
	}

	device := fields[0]
	mountPoint := UnescapeMountPath(fields[1])
	fsType := fields[2]
	options := fields[3]

	isReadOnly := false
	for _, opt := range strings.Split(options, ",") {
		if opt == "ro" {
			isReadOnly = true
			break
		}
	}

	majorMinor := getDeviceMajorMinor(device)

	return MountInfo{
		MountPoint:     mountPoint,
		Device:         device,
		FilesystemType: fsType,
		IsReadOnly:     isReadOnly,
		MajorMinor:     majorMinor,
	}, true
}

func getDeviceMajorMinor(device string) string {
	var stat syscall.Stat_t
	if err := syscall.Stat(device, &stat); err != nil {
		return ""
	}
	major := (stat.Rdev >> 8) & 0xff
	minor := stat.Rdev & 0xff
	return fmt.Sprintf("%d:%d", major, minor)
}

func parseMountInfoLine(line string) (MountInfo, bool) {
	parts := strings.Split(line, " - ")
	if len(parts) != 2 {
		return MountInfo{}, false
	}

	beforeDash := strings.Fields(parts[0])
	afterDash := strings.Fields(parts[1])

	if len(beforeDash) < 6 || len(afterDash) < 2 {
		return MountInfo{}, false
	}

	mountPoint := UnescapeMountPath(beforeDash[4])
	mountOptions := beforeDash[5]
	majorMinor := beforeDash[2]

	fsType := afterDash[0]
	device := afterDash[1]

	isReadOnly := false
	for _, opt := range strings.Split(mountOptions, ",") {
		if opt == "ro" {
			isReadOnly = true
			break
		}
	}

	return MountInfo{
		MountPoint:     mountPoint,
		Device:         device,
		FilesystemType: fsType,
		IsReadOnly:     isReadOnly,
		MajorMinor:     majorMinor,
	}, true
}

func UnescapeMountPath(path string) string {
	var result strings.Builder
	i := 0
	for i < len(path) {
		if path[i] == '\\' && i+3 < len(path) {
			octal := path[i+1 : i+4]
			if val, err := strconv.ParseInt(octal, 8, 32); err == nil {
				result.WriteByte(byte(val))
				i += 4
				continue
			}
		}
		result.WriteByte(path[i])
		i++
	}
	return result.String()
}
