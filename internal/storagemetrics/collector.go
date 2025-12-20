package storagemetrics

import (
	"context"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostlink/domain/metrics"
)

const (
	staleThreshold     = 3
	minElapsedSeconds  = 0.1 // 100ms minimum
	maxElapsedSeconds  = 300 // 5 minutes
)

func asValidFloat(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

type MountInfo struct {
	MountPoint     string
	Device         string
	FilesystemType string
	IsReadOnly     bool
	MajorMinor     string
}

type DiskIOStats struct {
	IOTimeMs        uint64
	ReadTimeMs      uint64
	WriteTimeMs     uint64
	SectorsRead     uint64
	SectorsWritten  uint64
	ReadsCompleted  uint64
	WritesCompleted uint64
}

type StatfsResult struct {
	Bsize  int64
	Blocks uint64
	Bfree  uint64
	Bavail uint64
	Files  uint64
	Ffree  uint64
}

type MountInfoReader interface {
	ReadMounts() ([]MountInfo, error)
}

type DiskStatsReader interface {
	ReadDiskStats() (map[string]DiskIOStats, error)
}

type StatfsProvider interface {
	Statfs(path string) (StatfsResult, error)
}

type StorageMetricSet struct {
	Attributes metrics.StorageAttributes
	Metrics    metrics.StorageMetrics
}

type Collector interface {
	Collect(ctx context.Context) ([]StorageMetricSet, error)
}

type Config struct {
	MountReader MountInfoReader
	StatsReader DiskStatsReader
	Statfs      StatfsProvider
}

type collector struct {
	mountReader           MountInfoReader
	statsReader           DiskStatsReader
	statfs                StatfsProvider
	lastStats             map[string]DiskIOStats
	lastSeen              map[string]time.Time
	missedSamples         map[string]int
	lastTime              time.Time
	previousSampleDevices map[string]bool
}

func New() Collector {
	return NewWithConfig(&Config{
		MountReader: NewMountInfoReader(),
		StatsReader: NewDiskStatsReader(),
		Statfs:      NewStatfsProvider(),
	})
}

func NewWithConfig(cfg *Config) Collector {
	c := &collector{
		lastStats:             make(map[string]DiskIOStats),
		lastSeen:              make(map[string]time.Time),
		missedSamples:         make(map[string]int),
		previousSampleDevices: make(map[string]bool),
	}
	if cfg != nil {
		c.mountReader = cfg.MountReader
		c.statsReader = cfg.StatsReader
		c.statfs = cfg.Statfs
	}
	return c
}

func (c *collector) Collect(ctx context.Context) ([]StorageMetricSet, error) {
	mounts, err := c.mountReader.ReadMounts()
	if err != nil {
		return nil, err
	}

	diskStats, err := c.statsReader.ReadDiskStats()
	if err != nil {
		diskStats = make(map[string]DiskIOStats)
	}

	now := time.Now()
	elapsed := now.Sub(c.lastTime).Seconds()
	elapsedMs := now.Sub(c.lastTime).Seconds() * 1000

	isFirstSample := c.lastTime.IsZero()
	isElapsedValid := elapsed >= minElapsedSeconds && elapsed < maxElapsedSeconds

	if !isFirstSample && !isElapsedValid {
		if elapsed < minElapsedSeconds {
			log.Printf("[WARN] storagemetrics: Negative or zero elapsed time (%.3fs), skipping deltas", elapsed)
		} else {
			log.Printf("[WARN] storagemetrics: Unusually large elapsed time: %.1fs, skipping deltas", elapsed)
		}
	}

	ioMetricsCache := make(map[string]metrics.StorageMetrics)
	currentDevices := make(map[string]bool)

	var results []StorageMetricSet

	for _, mount := range mounts {
		statfsResult, err := c.statfs.Statfs(mount.MountPoint)
		if err != nil {
			continue
		}

		// Check for invalid statfs results
		if statfsResult.Bsize == 0 && statfsResult.Blocks == 0 && statfsResult.Files == 0 {
			log.Printf("[WARN] storagemetrics: All capacity values zero for %s, skipping", mount.MountPoint)
			continue
		}
		if statfsResult.Bsize == 0 {
			log.Printf("[WARN] storagemetrics: Invalid block size (0) for %s, skipping capacity", mount.MountPoint)
		}

		var m metrics.StorageMetrics

		if statfsResult.Bsize > 0 {
			total := uint64(statfsResult.Blocks) * uint64(statfsResult.Bsize)
			free := uint64(statfsResult.Bavail) * uint64(statfsResult.Bsize)
			used := (uint64(statfsResult.Blocks) - uint64(statfsResult.Bfree)) * uint64(statfsResult.Bsize)

			m.DiskTotalBytes = asValidFloat(float64(total))
			m.DiskFreeBytes = asValidFloat(float64(free))
			m.DiskUsedBytes = asValidFloat(float64(used))

			denominator := used + free
			if denominator > 0 {
				m.DiskUsedPercent = asValidFloat(float64(used) / float64(denominator) * 100)
				m.DiskFreePercent = asValidFloat(100 - m.DiskUsedPercent)
			}
		}

		m.InodesTotal = statfsResult.Files
		m.InodesFree = statfsResult.Ffree
		m.InodesUsed = statfsResult.Files - statfsResult.Ffree
		if statfsResult.Files > 0 {
			m.InodesUsedPercent = asValidFloat(float64(m.InodesUsed) / float64(statfsResult.Files) * 100)
		}

		deviceKey := c.resolveDeviceKey(mount, diskStats)
		currentDevices[deviceKey] = true

		if cachedIO, ok := ioMetricsCache[deviceKey]; ok {
			m.TotalUtilizationPercent = cachedIO.TotalUtilizationPercent
			m.ReadUtilizationPercent = cachedIO.ReadUtilizationPercent
			m.WriteUtilizationPercent = cachedIO.WriteUtilizationPercent
			m.ReadBytesPerSecond = cachedIO.ReadBytesPerSecond
			m.WriteBytesPerSecond = cachedIO.WriteBytesPerSecond
			m.ReadWriteBytesPerSecond = cachedIO.ReadWriteBytesPerSecond
			m.ReadIOPerSecond = cachedIO.ReadIOPerSecond
			m.WriteIOPerSecond = cachedIO.WriteIOPerSecond
		} else {
			currentStats, hasStats := diskStats[deviceKey]
			canCalculateDelta := hasStats && !isFirstSample && isElapsedValid && c.wasSeenLastSample(deviceKey)

			if canCalculateDelta {
				lastStats, hadPrevious := c.lastStats[deviceKey]
				if hadPrevious && !c.hasCounterWrap(currentStats, lastStats) {
					ioTimeDelta := currentStats.IOTimeMs - lastStats.IOTimeMs
					readTimeDelta := currentStats.ReadTimeMs - lastStats.ReadTimeMs
					writeTimeDelta := currentStats.WriteTimeMs - lastStats.WriteTimeMs

					totalUtil := asValidFloat(float64(ioTimeDelta) / elapsedMs * 100)
					if totalUtil > 100 {
						totalUtil = 100
					}
					m.TotalUtilizationPercent = totalUtil

					totalTimeDelta := readTimeDelta + writeTimeDelta
					if totalTimeDelta > 0 {
						m.ReadUtilizationPercent = asValidFloat(totalUtil * (float64(readTimeDelta) / float64(totalTimeDelta)))
						m.WriteUtilizationPercent = asValidFloat(totalUtil * (float64(writeTimeDelta) / float64(totalTimeDelta)))
					}

					sectorsReadDelta := currentStats.SectorsRead - lastStats.SectorsRead
					sectorsWrittenDelta := currentStats.SectorsWritten - lastStats.SectorsWritten
					m.ReadBytesPerSecond = asValidFloat(float64(sectorsReadDelta*512) / elapsed)
					m.WriteBytesPerSecond = asValidFloat(float64(sectorsWrittenDelta*512) / elapsed)
					m.ReadWriteBytesPerSecond = asValidFloat(m.ReadBytesPerSecond + m.WriteBytesPerSecond)

					readsDelta := currentStats.ReadsCompleted - lastStats.ReadsCompleted
					writesDelta := currentStats.WritesCompleted - lastStats.WritesCompleted
					m.ReadIOPerSecond = asValidFloat(float64(readsDelta) / elapsed)
					m.WriteIOPerSecond = asValidFloat(float64(writesDelta) / elapsed)
				}
			}

			if hasStats {
				c.lastStats[deviceKey] = currentStats
				c.lastSeen[deviceKey] = now
			}

			ioMetricsCache[deviceKey] = metrics.StorageMetrics{
				TotalUtilizationPercent: m.TotalUtilizationPercent,
				ReadUtilizationPercent:  m.ReadUtilizationPercent,
				WriteUtilizationPercent: m.WriteUtilizationPercent,
				ReadBytesPerSecond:      m.ReadBytesPerSecond,
				WriteBytesPerSecond:     m.WriteBytesPerSecond,
				ReadWriteBytesPerSecond: m.ReadWriteBytesPerSecond,
				ReadIOPerSecond:         m.ReadIOPerSecond,
				WriteIOPerSecond:        m.WriteIOPerSecond,
			}
		}

		results = append(results, StorageMetricSet{
			Attributes: metrics.StorageAttributes{
				MountPoint:     mount.MountPoint,
				Device:         mount.Device,
				FilesystemType: mount.FilesystemType,
				IsReadOnly:     mount.IsReadOnly,
			},
			Metrics: m,
		})
	}

	c.cleanupStaleEntries(currentDevices)
	c.previousSampleDevices = currentDevices
	c.lastTime = now

	return results, nil
}

func (c *collector) resolveDeviceKey(mount MountInfo, diskStats map[string]DiskIOStats) string {
	device := mount.Device

	// Resolve /dev/root using major:minor from /proc/partitions
	if device == "/dev/root" && mount.MajorMinor != "" {
		if resolved := resolveDevRoot(mount.MajorMinor); resolved != "" {
			device = "/dev/" + resolved
		}
	}

	// Resolve symlinks (e.g., /dev/disk/by-uuid/... -> /dev/sda1)
	if resolved, err := filepath.EvalSymlinks(device); err == nil {
		device = resolved
	}

	// Try LVM resolution via major:minor
	if strings.HasPrefix(device, "/dev/mapper/") && mount.MajorMinor != "" {
		parts := strings.Split(mount.MajorMinor, ":")
		if len(parts) == 2 {
			dmKey := "dm-" + parts[1]
			if _, ok := diskStats[dmKey]; ok {
				return dmKey
			}
		}
	}

	return strings.TrimPrefix(device, "/dev/")
}

func resolveDevRoot(majorMinor string) string {
	content, err := os.ReadFile("/proc/partitions")
	if err != nil {
		return ""
	}
	return ParsePartitionsForDevice(string(content), majorMinor)
}

func ParsePartitionsForDevice(content, majorMinor string) string {
	parts := strings.Split(majorMinor, ":")
	if len(parts) != 2 {
		return ""
	}
	targetMajor, targetMinor := parts[0], parts[1]

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		major, minor, name := fields[0], fields[1], fields[3]
		if major == targetMajor && minor == targetMinor {
			return name
		}
	}
	return ""
}

func (c *collector) wasSeenLastSample(deviceKey string) bool {
	return c.previousSampleDevices[deviceKey]
}

func (c *collector) hasCounterWrap(current, previous DiskIOStats) bool {
	return current.ReadsCompleted < previous.ReadsCompleted ||
		current.WritesCompleted < previous.WritesCompleted ||
		current.SectorsRead < previous.SectorsRead ||
		current.SectorsWritten < previous.SectorsWritten ||
		current.IOTimeMs < previous.IOTimeMs
}

func (c *collector) cleanupStaleEntries(currentDevices map[string]bool) {
	for device := range c.lastSeen {
		if !currentDevices[device] {
			c.missedSamples[device]++
			if c.missedSamples[device] >= staleThreshold {
				delete(c.lastStats, device)
				delete(c.lastSeen, device)
				delete(c.missedSamples, device)
			}
		} else {
			c.missedSamples[device] = 0
		}
	}
}
