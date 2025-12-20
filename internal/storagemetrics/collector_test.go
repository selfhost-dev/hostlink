package storagemetrics

import (
	"context"
	"math"
	"os"
	"testing"
	"time"
)

func approxEqual(a, b, tolerance float64) bool {
	return math.Abs(a-b) <= tolerance
}

// Mock implementations

type mockMountReader struct {
	mounts []MountInfo
	err    error
}

func (m *mockMountReader) ReadMounts() ([]MountInfo, error) {
	return m.mounts, m.err
}

type mockDiskStatsReader struct {
	stats map[string]DiskIOStats
	err   error
}

func (m *mockDiskStatsReader) ReadDiskStats() (map[string]DiskIOStats, error) {
	return m.stats, m.err
}

type mockStatfsProvider struct {
	results map[string]StatfsResult
	err     error
}

func (m *mockStatfsProvider) Statfs(path string) (StatfsResult, error) {
	if m.err != nil {
		return StatfsResult{}, m.err
	}
	return m.results[path], nil
}

// Mount parsing tests

// Parses a basic ext4 mount from /proc/self/mountinfo format
func TestParseMountInfo_ParsesBasicMount(t *testing.T) {
	content := `36 35 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw`

	mounts, err := ParseMountInfo(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}

	m := mounts[0]
	if m.MountPoint != "/" {
		t.Errorf("expected mount point '/', got %q", m.MountPoint)
	}
	if m.Device != "/dev/sda1" {
		t.Errorf("expected device '/dev/sda1', got %q", m.Device)
	}
	if m.FilesystemType != "ext4" {
		t.Errorf("expected filesystem 'ext4', got %q", m.FilesystemType)
	}
	if m.IsReadOnly {
		t.Error("expected read-write mount")
	}
}

// Filters out unsupported filesystems like tmpfs, proc, sysfs
func TestParseMountInfo_FiltersUnsupportedFilesystems(t *testing.T) {
	content := `22 1 0:21 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
23 1 0:22 / /sys rw,nosuid,nodev,noexec,relatime - sysfs sysfs rw
24 1 0:5 / /dev rw,nosuid,relatime - devtmpfs devtmpfs rw
25 1 0:23 / /run rw,nosuid,nodev - tmpfs tmpfs rw
36 35 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw`

	mounts, err := ParseMountInfo(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount (only ext4), got %d", len(mounts))
	}
	if mounts[0].FilesystemType != "ext4" {
		t.Errorf("expected ext4, got %q", mounts[0].FilesystemType)
	}
}

// Detects read-only mounts from mount options
func TestParseMountInfo_DetectsReadOnlyMounts(t *testing.T) {
	content := `36 35 8:1 / /mnt/readonly ro,relatime shared:1 - ext4 /dev/sda1 ro`

	mounts, err := ParseMountInfo(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if !mounts[0].IsReadOnly {
		t.Error("expected read-only mount")
	}
}

// Parses /proc/mounts format correctly
func TestParseMounts_ParsesBasicMount(t *testing.T) {
	content := `/dev/sda1 / ext4 rw,relatime 0 1`

	mounts, err := ParseMounts(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	m := mounts[0]
	if m.Device != "/dev/sda1" {
		t.Errorf("expected device /dev/sda1, got %s", m.Device)
	}
	if m.MountPoint != "/" {
		t.Errorf("expected mount point /, got %s", m.MountPoint)
	}
	if m.FilesystemType != "ext4" {
		t.Errorf("expected filesystem ext4, got %s", m.FilesystemType)
	}
	if m.IsReadOnly {
		t.Error("expected read-write mount")
	}
}

// Detects read-only mounts in /proc/mounts format
func TestParseMounts_DetectsReadOnlyMounts(t *testing.T) {
	content := `/dev/sda1 /mnt/backup ext4 ro,noexec 0 0`

	mounts, err := ParseMounts(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if !mounts[0].IsReadOnly {
		t.Error("expected read-only mount")
	}
}

// Mount source fallback tests

// Uses /proc/self/mountinfo when available
func TestReadMounts_UsesMountInfoFirst(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path == "/proc/self/mountinfo" {
			return []byte(`36 35 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw`), nil
		}
		return nil, os.ErrNotExist
	}

	mounts, err := readMountsWithReader(readFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].MajorMinor != "8:1" {
		t.Error("expected MajorMinor from mountinfo format")
	}
}

// Falls back to /proc/self/mounts when mountinfo fails
func TestReadMounts_FallsBackToMounts(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path == "/proc/self/mountinfo" {
			return nil, os.ErrNotExist
		}
		if path == "/proc/self/mounts" {
			return []byte(`/dev/fake-test-device / ext4 rw,relatime 0 1`), nil
		}
		return nil, os.ErrNotExist
	}

	mounts, err := readMountsWithReader(readFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].MajorMinor != "" {
		t.Error("expected empty MajorMinor from mounts format")
	}
}

// Falls back to /etc/mtab when both mountinfo and mounts fail
func TestReadMounts_FallsBackToMtab(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		if path == "/proc/self/mountinfo" {
			return nil, os.ErrNotExist
		}
		if path == "/proc/self/mounts" {
			return nil, os.ErrNotExist
		}
		if path == "/etc/mtab" {
			return []byte(`/dev/fake-test-device / ext4 rw,relatime 0 1`), nil
		}
		return nil, os.ErrNotExist
	}

	mounts, err := readMountsWithReader(readFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
}

// Returns error when all mount sources fail
func TestReadMounts_AllSourcesFail(t *testing.T) {
	readFile := func(path string) ([]byte, error) {
		return nil, os.ErrNotExist
	}

	_, err := readMountsWithReader(readFile)
	if err == nil {
		t.Error("expected error when all sources fail")
	}
}

// Diskstats tests

// Parses standard device like sda1 from /proc/diskstats
func TestParseDiskStats_ParsesStandardDevice(t *testing.T) {
	content := `   8       1 sda1 12345 1234 567890 12000 54321 4321 234567 8000 0 15000 20000`

	stats, err := ParseDiskStats(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := stats["sda1"]
	if !ok {
		t.Fatal("expected sda1 in stats")
	}
	if s.ReadsCompleted != 12345 {
		t.Errorf("expected reads 12345, got %d", s.ReadsCompleted)
	}
	if s.SectorsRead != 567890 {
		t.Errorf("expected sectors read 567890, got %d", s.SectorsRead)
	}
	if s.WritesCompleted != 54321 {
		t.Errorf("expected writes 54321, got %d", s.WritesCompleted)
	}
	if s.SectorsWritten != 234567 {
		t.Errorf("expected sectors written 234567, got %d", s.SectorsWritten)
	}
	if s.IOTimeMs != 15000 {
		t.Errorf("expected io time 15000, got %d", s.IOTimeMs)
	}
}

// Parses NVMe device like nvme0n1p1 from /proc/diskstats
func TestParseDiskStats_ParsesNVMeDevice(t *testing.T) {
	content := ` 259       1 nvme0n1p1 5000 500 100000 5000 3000 300 60000 3000 0 8000 8000`

	stats, err := ParseDiskStats(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := stats["nvme0n1p1"]
	if !ok {
		t.Fatal("expected nvme0n1p1 in stats")
	}
	if s.ReadsCompleted != 5000 {
		t.Errorf("expected reads 5000, got %d", s.ReadsCompleted)
	}
}

// Parses LVM device like dm-0 from /proc/diskstats
func TestParseDiskStats_ParsesLVMDevice(t *testing.T) {
	content := ` 253       0 dm-0 8000 0 160000 4000 6000 0 120000 3000 0 7000 7000`

	stats, err := ParseDiskStats(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := stats["dm-0"]
	if !ok {
		t.Fatal("expected dm-0 in stats")
	}
	if s.ReadsCompleted != 8000 {
		t.Errorf("expected reads 8000, got %d", s.ReadsCompleted)
	}
}

// Partitions parsing tests

// Resolves device name from /proc/partitions by major:minor
func TestParsePartitionsForDevice_FindsDevice(t *testing.T) {
	content := `major minor  #blocks  name

   8        0  500107608 sda
   8        1     512000 sda1
   8        2  499594240 sda2
 253        0  104857600 dm-0
 253        1  394735616 dm-1`

	name := ParsePartitionsForDevice(content, "8:1")
	if name != "sda1" {
		t.Errorf("expected sda1, got %s", name)
	}
}

// Returns empty string when device not found
func TestParsePartitionsForDevice_NotFound(t *testing.T) {
	content := `major minor  #blocks  name

   8        0  500107608 sda`

	name := ParsePartitionsForDevice(content, "9:0")
	if name != "" {
		t.Errorf("expected empty string, got %s", name)
	}
}

// Returns empty string for invalid major:minor format
func TestParsePartitionsForDevice_InvalidFormat(t *testing.T) {
	content := `major minor  #blocks  name

   8        0  500107608 sda`

	name := ParsePartitionsForDevice(content, "invalid")
	if name != "" {
		t.Errorf("expected empty string, got %s", name)
	}
}

// Capacity tests

// Returns capacity metrics from statfs for a mount point
func TestCollect_ReturnsCapacityMetrics(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{
				MountPoint:     "/",
				Device:         "/dev/sda1",
				FilesystemType: "ext4",
			}},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{"sda1": {}},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000000, Bfree: 400000, Bavail: 350000, Files: 100000, Ffree: 90000},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	m := results[0].Metrics
	expectedTotal := float64(1000000 * 4096)
	expectedFree := float64(350000 * 4096)
	expectedUsed := float64((1000000 - 400000) * 4096)

	if m.DiskTotalBytes != expectedTotal {
		t.Errorf("expected total %v, got %v", expectedTotal, m.DiskTotalBytes)
	}
	if m.DiskFreeBytes != expectedFree {
		t.Errorf("expected free %v, got %v", expectedFree, m.DiskFreeBytes)
	}
	if m.DiskUsedBytes != expectedUsed {
		t.Errorf("expected used %v, got %v", expectedUsed, m.DiskUsedBytes)
	}
}

// Calculates used percent as used/(used+available)*100
func TestCollect_CalculatesUsedPercent(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	used := float64((1000 - 400) * 4096)
	free := float64(300 * 4096)
	expectedPercent := used / (used + free) * 100

	if m.DiskUsedPercent != expectedPercent {
		t.Errorf("expected used percent %v, got %v", expectedPercent, m.DiskUsedPercent)
	}
}

// Calculates free percent as 100 - used_percent
func TestCollect_CalculatesFreePercent(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	expectedFreePercent := 100 - m.DiskUsedPercent

	if !approxEqual(m.DiskFreePercent, expectedFreePercent, 0.0001) {
		t.Errorf("expected free percent %v, got %v", expectedFreePercent, m.DiskFreePercent)
	}
}

// Calculates inode metrics from statfs
func TestCollect_CalculatesInodeMetrics(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 10000, Ffree: 8000},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.InodesTotal != 10000 {
		t.Errorf("expected inodes total 10000, got %d", m.InodesTotal)
	}
	if m.InodesFree != 8000 {
		t.Errorf("expected inodes free 8000, got %d", m.InodesFree)
	}
	if m.InodesUsed != 2000 {
		t.Errorf("expected inodes used 2000, got %d", m.InodesUsed)
	}
	expectedPercent := float64(2000) / float64(10000) * 100
	if m.InodesUsedPercent != expectedPercent {
		t.Errorf("expected inodes used percent %v, got %v", expectedPercent, m.InodesUsedPercent)
	}
}

// I/O metrics tests

// Returns zero for I/O metrics on first sample (no delta available)
func TestCollect_ReturnsZeroOnFirstSample(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{
				"sda1": {ReadsCompleted: 1000, WritesCompleted: 500, SectorsRead: 20000, SectorsWritten: 10000, IOTimeMs: 5000},
			},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.ReadIOPerSecond != 0 {
		t.Errorf("expected read IOPS 0 on first sample, got %v", m.ReadIOPerSecond)
	}
	if m.WriteIOPerSecond != 0 {
		t.Errorf("expected write IOPS 0 on first sample, got %v", m.WriteIOPerSecond)
	}
	if m.ReadBytesPerSecond != 0 {
		t.Errorf("expected read bytes/sec 0 on first sample, got %v", m.ReadBytesPerSecond)
	}
	if m.TotalUtilizationPercent != 0 {
		t.Errorf("expected utilization 0 on first sample, got %v", m.TotalUtilizationPercent)
	}
}

// Calculates delta-based I/O metrics on second sample
func TestCollect_CalculatesDeltaOnSecondSample(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {ReadsCompleted: 1000, WritesCompleted: 500, SectorsRead: 20000, SectorsWritten: 10000, IOTimeMs: 5000, ReadTimeMs: 3000, WriteTimeMs: 2000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {ReadsCompleted: 1100, WritesCompleted: 550, SectorsRead: 22000, SectorsWritten: 11000, IOTimeMs: 6000, ReadTimeMs: 3600, WriteTimeMs: 2400},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.ReadIOPerSecond == 0 {
		t.Error("expected non-zero read IOPS on second sample")
	}
	if m.WriteIOPerSecond == 0 {
		t.Error("expected non-zero write IOPS on second sample")
	}
}

// Calculates utilization percent from io_time delta
func TestCollect_CalculatesUtilizationPercent(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {IOTimeMs: 5000, ReadTimeMs: 3000, WriteTimeMs: 2000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {IOTimeMs: 6000, ReadTimeMs: 3600, WriteTimeMs: 2400},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	expectedUtil := float64(1000) / float64(10000) * 100
	if !approxEqual(m.TotalUtilizationPercent, expectedUtil, 0.01) {
		t.Errorf("expected utilization %v%%, got %v%%", expectedUtil, m.TotalUtilizationPercent)
	}
}

// Splits total utilization into read/write based on time ratio
func TestCollect_CalculatesReadWriteUtilizationSplit(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {IOTimeMs: 5000, ReadTimeMs: 3000, WriteTimeMs: 2000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {IOTimeMs: 6000, ReadTimeMs: 3600, WriteTimeMs: 2400},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	totalUtil := m.TotalUtilizationPercent
	readTimeDelta := float64(600)
	writeTimeDelta := float64(400)
	totalTimeDelta := readTimeDelta + writeTimeDelta
	expectedReadUtil := totalUtil * (readTimeDelta / totalTimeDelta)
	expectedWriteUtil := totalUtil * (writeTimeDelta / totalTimeDelta)

	if m.ReadUtilizationPercent != expectedReadUtil {
		t.Errorf("expected read utilization %v%%, got %v%%", expectedReadUtil, m.ReadUtilizationPercent)
	}
	if m.WriteUtilizationPercent != expectedWriteUtil {
		t.Errorf("expected write utilization %v%%, got %v%%", expectedWriteUtil, m.WriteUtilizationPercent)
	}
}

// Calculates throughput in bytes per second from sectors delta
func TestCollect_CalculatesThroughput(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {SectorsRead: 20000, SectorsWritten: 10000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {SectorsRead: 22000, SectorsWritten: 11000},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	expectedReadBytes := float64(2000*512) / 10.0
	expectedWriteBytes := float64(1000*512) / 10.0

	if !approxEqual(m.ReadBytesPerSecond, expectedReadBytes, 1.0) {
		t.Errorf("expected read bytes/sec %v, got %v", expectedReadBytes, m.ReadBytesPerSecond)
	}
	if !approxEqual(m.WriteBytesPerSecond, expectedWriteBytes, 1.0) {
		t.Errorf("expected write bytes/sec %v, got %v", expectedWriteBytes, m.WriteBytesPerSecond)
	}
}

// Calculates total throughput as sum of read + write bytes per second
func TestCollect_CalculatesTotalThroughput(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {SectorsRead: 20000, SectorsWritten: 10000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {SectorsRead: 22000, SectorsWritten: 11000},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	expectedTotal := m.ReadBytesPerSecond + m.WriteBytesPerSecond

	if m.ReadWriteBytesPerSecond != expectedTotal {
		t.Errorf("expected total bytes/sec %v, got %v", expectedTotal, m.ReadWriteBytesPerSecond)
	}
}

// Calculates IOPS from reads/writes completed delta
func TestCollect_CalculatesIOPS(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {ReadsCompleted: 1000, WritesCompleted: 500},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {ReadsCompleted: 1100, WritesCompleted: 550},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	expectedReadIOPS := float64(100) / 10.0
	expectedWriteIOPS := float64(50) / 10.0

	if !approxEqual(m.ReadIOPerSecond, expectedReadIOPS, 0.01) {
		t.Errorf("expected read IOPS %v, got %v", expectedReadIOPS, m.ReadIOPerSecond)
	}
	if !approxEqual(m.WriteIOPerSecond, expectedWriteIOPS, 0.01) {
		t.Errorf("expected write IOPS %v, got %v", expectedWriteIOPS, m.WriteIOPerSecond)
	}
}

// Multiple mounts tests

// Reports each mount point as a separate metric set
func TestCollect_ReportsEachMountSeparately(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{
				{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"},
				{MountPoint: "/home", Device: "/dev/sda2", FilesystemType: "ext4"},
			},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{
				"sda1": {},
				"sda2": {},
			},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/":     {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
				"/home": {Bsize: 4096, Blocks: 2000, Bfree: 1000, Bavail: 900, Files: 2000, Ffree: 1800},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	mountPoints := make(map[string]bool)
	for _, r := range results {
		mountPoints[r.Attributes.MountPoint] = true
	}

	if !mountPoints["/"] {
		t.Error("expected / mount point in results")
	}
	if !mountPoints["/home"] {
		t.Error("expected /home mount point in results")
	}
}

// Shares I/O metrics for multiple mounts on same device
func TestCollect_SharesIOMetricsForSameDevice(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {ReadsCompleted: 1000, WritesCompleted: 500, SectorsRead: 20000, SectorsWritten: 10000, IOTimeMs: 5000, ReadTimeMs: 3000, WriteTimeMs: 2000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{
				{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"},
				{MountPoint: "/var", Device: "/dev/sda1", FilesystemType: "ext4"},
			},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/":    {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
				"/var": {Bsize: 4096, Blocks: 500, Bfree: 200, Bavail: 150, Files: 500, Ffree: 450},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {ReadsCompleted: 1100, WritesCompleted: 550, SectorsRead: 22000, SectorsWritten: 11000, IOTimeMs: 6000, ReadTimeMs: 3600, WriteTimeMs: 2400},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Metrics.ReadIOPerSecond != results[1].Metrics.ReadIOPerSecond {
		t.Error("expected same read IOPS for both mounts on same device")
	}
	if results[0].Metrics.WriteIOPerSecond != results[1].Metrics.WriteIOPerSecond {
		t.Error("expected same write IOPS for both mounts on same device")
	}

	if results[0].Metrics.DiskTotalBytes == results[1].Metrics.DiskTotalBytes {
		t.Error("expected different capacity metrics for different mount points")
	}
}

// Edge case tests

// Unescapes octal sequences like \040 to space in mount paths
func TestUnescapeMountPath_UnescapesSpace(t *testing.T) {
	input := `/mnt/My\040Documents`
	expected := `/mnt/My Documents`

	result := UnescapeMountPath(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// Unescapes multiple octal sequences in a single path
func TestUnescapeMountPath_UnescapesMultipleSequences(t *testing.T) {
	input := `/mnt/My\040Documents\040And\011Tabs`
	expected := "/mnt/My Documents And\tTabs"

	result := UnescapeMountPath(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// Leaves paths without escape sequences unchanged
func TestUnescapeMountPath_LeavesNormalPathUnchanged(t *testing.T) {
	input := `/mnt/normal/path`
	expected := `/mnt/normal/path`

	result := UnescapeMountPath(input)
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

// Resolves symlink device paths to actual device for diskstats lookup
func TestCollect_ResolvesSymlinkDevicePath(t *testing.T) {
	// Note: This test requires DeviceResolver interface to be added to Config
	// For now, test that symlink-style paths without diskstats entry return 0 I/O
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{
				MountPoint:     "/data",
				Device:         "/dev/disk/by-uuid/abcd-1234",
				FilesystemType: "ext4",
			}},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{
				"sda1": {ReadsCompleted: 1000},
			},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/data": {Bsize: 4096, Blocks: 1000, Bfree: 500, Bavail: 400, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Device path preserved in output
	if results[0].Attributes.Device != "/dev/disk/by-uuid/abcd-1234" {
		t.Errorf("expected original device path, got %q", results[0].Attributes.Device)
	}
}

// Returns zero for I/O metrics when counter wraps (current < previous)
func TestCollect_HandlesCounterWrap(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {ReadsCompleted: 1000, WritesCompleted: 500, SectorsRead: 20000, SectorsWritten: 10000, IOTimeMs: 5000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	// Counter wraps: current < previous
	statsReader.stats = map[string]DiskIOStats{
		"sda1": {ReadsCompleted: 100, WritesCompleted: 50, SectorsRead: 2000, SectorsWritten: 1000, IOTimeMs: 500},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.ReadIOPerSecond != 0 {
		t.Errorf("expected 0 for wrapped counter, got %v", m.ReadIOPerSecond)
	}
	if m.WriteIOPerSecond != 0 {
		t.Errorf("expected 0 for wrapped counter, got %v", m.WriteIOPerSecond)
	}
}

// Skips I/O delta calculation when mount was missing in previous sample
func TestCollect_SkipsIOOnMountReappear(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {ReadsCompleted: 1000, WritesCompleted: 500},
		},
	}
	mountReader := &mockMountReader{
		mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
	}

	cfg := &Config{
		MountReader: mountReader,
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)

	// First sample
	c.Collect(context.Background())

	// Mount disappears
	mountReader.mounts = []MountInfo{}
	c.lastTime = time.Now().Add(-10 * time.Second)
	c.Collect(context.Background())

	// Mount reappears with new counters
	mountReader.mounts = []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}}
	statsReader.stats = map[string]DiskIOStats{
		"sda1": {ReadsCompleted: 2000, WritesCompleted: 1000},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	// Should return 0 because mount was missing in previous sample
	if m.ReadIOPerSecond != 0 {
		t.Errorf("expected 0 after mount reappear, got %v", m.ReadIOPerSecond)
	}
}

// Removes stale device entries after missing 3 consecutive samples
func TestCollect_CleansUpStaleDeviceState(t *testing.T) {
	mountReader := &mockMountReader{
		mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
	}

	cfg := &Config{
		MountReader: mountReader,
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{"sda1": {ReadsCompleted: 1000}},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	if _, ok := c.lastStats["sda1"]; !ok {
		t.Fatal("expected sda1 in lastStats after first sample")
	}

	// Device disappears for 3 samples
	mountReader.mounts = []MountInfo{}
	for range 3 {
		c.lastTime = time.Now().Add(-10 * time.Second)
		c.Collect(context.Background())
	}

	// State should be cleaned up
	if _, ok := c.lastStats["sda1"]; ok {
		t.Error("expected sda1 to be removed from lastStats after 3 missed samples")
	}
}

// Returns zero for rates when elapsed time is zero or negative
func TestCollect_HandlesZeroElapsedTime(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {ReadsCompleted: 1000, WritesCompleted: 500},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {ReadsCompleted: 1100, WritesCompleted: 550},
	}
	// Set lastTime to now (zero elapsed)
	c.lastTime = time.Now()

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.ReadIOPerSecond != 0 {
		t.Errorf("expected 0 for zero elapsed time, got %v", m.ReadIOPerSecond)
	}
}

// Returns zero for rates when elapsed time exceeds threshold
func TestCollect_HandlesLargeElapsedTime(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {ReadsCompleted: 1000, WritesCompleted: 500},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	statsReader.stats = map[string]DiskIOStats{
		"sda1": {ReadsCompleted: 1100, WritesCompleted: 550},
	}
	// Set lastTime to 1 hour ago (exceeds reasonable threshold)
	c.lastTime = time.Now().Add(-1 * time.Hour)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.ReadIOPerSecond != 0 {
		t.Errorf("expected 0 for large elapsed time, got %v", m.ReadIOPerSecond)
	}
}

// Skips capacity metrics when block size is zero
func TestCollect_SkipsCapacityOnZeroBlockSize(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 0, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	m := results[0].Metrics
	if m.DiskTotalBytes != 0 {
		t.Errorf("expected 0 for zero block size, got %v", m.DiskTotalBytes)
	}
	if m.DiskUsedPercent != 0 {
		t.Errorf("expected 0 for zero block size, got %v", m.DiskUsedPercent)
	}
}

// Returns zero percent when denominator is zero (division protection)
func TestCollect_HandlesDivisionByZero(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				// Bfree == Blocks means used = 0, and Bavail = 0 means free = 0
				// So denominator (used + free) = 0
				"/": {Bsize: 4096, Blocks: 100, Bfree: 100, Bavail: 0, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	// Should not panic or return NaN
	if m.DiskUsedPercent != 0 {
		t.Errorf("expected 0 for zero denominator, got %v", m.DiskUsedPercent)
	}
}

// Caps utilization at 100% when calculation exceeds 100
func TestCollect_CapsUtilizationAt100Percent(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {IOTimeMs: 0},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	// IOTimeMs delta exceeds elapsed time (should result in >100% without capping)
	statsReader.stats = map[string]DiskIOStats{
		"sda1": {IOTimeMs: 20000}, // 20 seconds of IO time
	}
	c.lastTime = time.Now().Add(-10 * time.Second) // But only 10 seconds elapsed

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.TotalUtilizationPercent > 100 {
		t.Errorf("expected utilization capped at 100, got %v", m.TotalUtilizationPercent)
	}
	if m.TotalUtilizationPercent != 100 {
		t.Errorf("expected utilization to be 100, got %v", m.TotalUtilizationPercent)
	}
}

// Returns zero I/O metrics when device is not found in diskstats
func TestCollect_ReturnsZeroIOWhenDeviceNotInDiskstats(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{
				"sdb1": {ReadsCompleted: 1000}, // Different device
			},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Capacity should still be reported
	if results[0].Metrics.DiskTotalBytes == 0 {
		t.Error("expected capacity metrics to be reported")
	}

	// I/O should be zero
	if results[0].Metrics.ReadIOPerSecond != 0 {
		t.Errorf("expected 0 I/O when device not in diskstats, got %v", results[0].Metrics.ReadIOPerSecond)
	}
}

// Resolves LVM device path to dm-X using major:minor from mountinfo
func TestCollect_ResolvesLVMDeviceViaMajorMinor(t *testing.T) {
	// LVM device with major:minor that maps to dm-0
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{
				MountPoint:     "/",
				Device:         "/dev/mapper/vg-lv",
				FilesystemType: "ext4",
				MajorMinor:     "253:0",
			}},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{
				"dm-0": {ReadsCompleted: 1000, WritesCompleted: 500},
			},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	// Verify dm-0 was tracked
	if _, ok := c.lastStats["dm-0"]; !ok {
		t.Error("expected dm-0 to be tracked in lastStats for LVM device")
	}
}

// Handles petabyte-scale filesystem values without overflow
func TestCollect_HandlesLargeFilesystemValues(t *testing.T) {
	// 1 PB filesystem: 1024^5 bytes = 1,125,899,906,842,624 bytes
	// With 4096 byte blocks: ~274,877,906,944 blocks
	blocks := uint64(274877906944)
	bsize := int64(4096)

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: bsize, Blocks: blocks, Bfree: blocks / 2, Bavail: blocks / 2, Files: 1000000000, Ffree: 900000000},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	expectedTotal := float64(blocks) * float64(bsize)

	if m.DiskTotalBytes != expectedTotal {
		t.Errorf("expected total %v, got %v", expectedTotal, m.DiskTotalBytes)
	}

	// Verify no overflow (value should be approximately 1 PB)
	onePB := float64(1125899906842624)
	if m.DiskTotalBytes < onePB*0.99 || m.DiskTotalBytes > onePB*1.01 {
		t.Errorf("expected approximately 1 PB, got %v", m.DiskTotalBytes)
	}
}

// Returns zero percent when inodes_total is zero
func TestCollect_HandlesZeroInodeTotal(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 0, Ffree: 0},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	if m.InodesUsedPercent != 0 {
		t.Errorf("expected 0 for zero inode total, got %v", m.InodesUsedPercent)
	}
}

// Returns zero for read/write utilization split when time deltas are zero
func TestCollect_HandlesZeroReadWriteTimeDelta(t *testing.T) {
	statsReader := &mockDiskStatsReader{
		stats: map[string]DiskIOStats{
			"sda1": {IOTimeMs: 5000, ReadTimeMs: 3000, WriteTimeMs: 2000},
		},
	}

	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: statsReader,
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg).(*collector)
	c.Collect(context.Background())

	// Same read/write time (zero delta) but different IO time
	statsReader.stats = map[string]DiskIOStats{
		"sda1": {IOTimeMs: 6000, ReadTimeMs: 3000, WriteTimeMs: 2000},
	}
	c.lastTime = time.Now().Add(-10 * time.Second)

	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics
	// Total utilization should still be calculated
	if m.TotalUtilizationPercent == 0 {
		t.Error("expected non-zero total utilization")
	}
	// But read/write split should be zero (no way to split)
	if m.ReadUtilizationPercent != 0 {
		t.Errorf("expected 0 for read utilization with zero time delta, got %v", m.ReadUtilizationPercent)
	}
	if m.WriteUtilizationPercent != 0 {
		t.Errorf("expected 0 for write utilization with zero time delta, got %v", m.WriteUtilizationPercent)
	}
}

// Returns zero for NaN or Inf float values
func TestCollect_HandlesNaNAndInfValues(t *testing.T) {
	// This test verifies the collector doesn't produce NaN/Inf
	// by testing edge cases that could cause them
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"}},
		},
		StatsReader: &mockDiskStatsReader{stats: map[string]DiskIOStats{"sda1": {}}},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				// All zeros - could produce NaN in percentage calculations
				"/": {Bsize: 4096, Blocks: 0, Bfree: 0, Bavail: 0, Files: 0, Ffree: 0},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := results[0].Metrics

	// Verify no NaN or Inf values
	if math.IsNaN(m.DiskUsedPercent) || math.IsInf(m.DiskUsedPercent, 0) {
		t.Error("DiskUsedPercent is NaN or Inf")
	}
	if math.IsNaN(m.DiskFreePercent) || math.IsInf(m.DiskFreePercent, 0) {
		t.Error("DiskFreePercent is NaN or Inf")
	}
	if math.IsNaN(m.InodesUsedPercent) || math.IsInf(m.InodesUsedPercent, 0) {
		t.Error("InodesUsedPercent is NaN or Inf")
	}
}

// Skips mount point entirely when statfs fails
func TestCollect_SkipsWhenStatfsFails(t *testing.T) {
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{
				{MountPoint: "/", Device: "/dev/sda1", FilesystemType: "ext4"},
				{MountPoint: "/data", Device: "/dev/sdb1", FilesystemType: "ext4"},
			},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{"sda1": {}, "sdb1": {}},
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
				// /data not in results - simulates statfs failure
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only have 1 result (/ succeeded, /data failed)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Attributes.MountPoint != "/" {
		t.Errorf("expected / mount point, got %q", results[0].Attributes.MountPoint)
	}
}

// Returns zero I/O when device symlink is broken
func TestCollect_HandlesBrokenSymlink(t *testing.T) {
	// Broken symlink results in device not found in diskstats
	// Same behavior as device not in diskstats
	cfg := &Config{
		MountReader: &mockMountReader{
			mounts: []MountInfo{{
				MountPoint:     "/data",
				Device:         "/dev/disk/by-uuid/nonexistent",
				FilesystemType: "ext4",
			}},
		},
		StatsReader: &mockDiskStatsReader{
			stats: map[string]DiskIOStats{}, // Empty - device not found
		},
		Statfs: &mockStatfsProvider{
			results: map[string]StatfsResult{
				"/data": {Bsize: 4096, Blocks: 1000, Bfree: 400, Bavail: 300, Files: 1000, Ffree: 900},
			},
		},
	}

	c := NewWithConfig(cfg)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Capacity should still be reported
	if results[0].Metrics.DiskTotalBytes == 0 {
		t.Error("expected capacity metrics to be reported")
	}

	// I/O should be zero
	if results[0].Metrics.ReadIOPerSecond != 0 {
		t.Errorf("expected 0 I/O for broken symlink, got %v", results[0].Metrics.ReadIOPerSecond)
	}
}
