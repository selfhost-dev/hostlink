package storagemetrics

import (
	"os"
	"strconv"
	"strings"
)

type diskStatsReader struct{}

func NewDiskStatsReader() DiskStatsReader {
	return &diskStatsReader{}
}

func (r *diskStatsReader) ReadDiskStats() (map[string]DiskIOStats, error) {
	content, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	return ParseDiskStats(string(content))
}

func ParseDiskStats(content string) (map[string]DiskIOStats, error) {
	stats := make(map[string]DiskIOStats)

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}

		deviceName := fields[2]

		readsCompleted, _ := strconv.ParseUint(fields[3], 10, 64)
		sectorsRead, _ := strconv.ParseUint(fields[5], 10, 64)
		readTimeMs, _ := strconv.ParseUint(fields[6], 10, 64)
		writesCompleted, _ := strconv.ParseUint(fields[7], 10, 64)
		sectorsWritten, _ := strconv.ParseUint(fields[9], 10, 64)
		writeTimeMs, _ := strconv.ParseUint(fields[10], 10, 64)
		ioTimeMs, _ := strconv.ParseUint(fields[12], 10, 64)

		stats[deviceName] = DiskIOStats{
			ReadsCompleted:  readsCompleted,
			SectorsRead:     sectorsRead,
			ReadTimeMs:      readTimeMs,
			WritesCompleted: writesCompleted,
			SectorsWritten:  sectorsWritten,
			WriteTimeMs:     writeTimeMs,
			IOTimeMs:        ioTimeMs,
		}
	}

	return stats, nil
}
