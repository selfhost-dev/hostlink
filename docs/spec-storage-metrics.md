# Storage Metrics Collector - Technical Specification

## Overview

The storage metrics collector gathers disk and filesystem metrics from Linux systems and sends them to an API endpoint. Each mount point generates a separate metric set entry.

## Data Sources

### Primary Sources

| Source | Purpose |
|--------|---------|
| `/proc/self/mountinfo` | Mount point enumeration (preferred) |
| `/proc/self/mounts` | Fallback mount enumeration |
| `/etc/mtab` | Secondary fallback mount enumeration |
| `/proc/diskstats` | I/O statistics (utilization, throughput, IOPS) |
| `statfs()` syscall | Space and inode metrics |
| `/proc/partitions` | Device major:minor resolution |

### Mount Info File Formats

**`/proc/self/mountinfo` format:**
```
36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
```
- Field 5 (index 4): mount point (`/mnt2`)
- After `-` separator: filesystem type (`ext3`), device (`/dev/root`)
- Mount options in field 6

**`/proc/self/mounts` and `/etc/mtab` format:**
```
/dev/sda1 / ext4 rw,relatime 0 0
```
- Field 1 (index 0): device
- Field 2 (index 1): mount point
- Field 3 (index 2): filesystem type
- Field 4 (index 3): mount options

### `/proc/diskstats` Format

```
   8       0 sda 12345 1234 567890 12345 ...
```

| Field | Index | Description | Unit |
|-------|-------|-------------|------|
| 1 | 0 | Major number | - |
| 2 | 1 | Minor number | - |
| 3 | 2 | Device name | string |
| 4 | 3 | Reads completed | count |
| 5 | 4 | Reads merged | count |
| 6 | 5 | Sectors read | sectors |
| 7 | 6 | Read time | ms |
| 8 | 7 | Writes completed | count |
| 9 | 8 | Writes merged | count |
| 10 | 9 | Sectors written | sectors |
| 11 | 10 | Write time | ms |
| 12 | 11 | I/Os in progress | count |
| 13 | 12 | I/O time | ms |
| 14 | 13 | Weighted I/O time | ms |

---

## Field Definitions

### Static Labels (per mount point)

| Field | Type | Source | Extraction Method |
|-------|------|--------|-------------------|
| `mount_point` | string | mountinfo/mounts | Field index 4 (mountinfo) or 1 (mounts) |
| `device` | string | mountinfo/mounts | After `-` separator (mountinfo) or field 0 (mounts) |
| `filesystem_type` | string | mountinfo/mounts | After device in mountinfo, or field 2 in mounts |
| `is_read_only` | bool | mount options | `true` if "ro" present in options, else `false` |

**Supported Filesystem Types:**
Only collect metrics for: `xfs`, `btrfs`, `ext`, `ext2`, `ext3`, `ext4`, `hfs`, `vxfs`, `reiserfs`

Skip mount points with unsupported filesystem types.

**Skipped Filesystem Types:**
- Virtual filesystems: `proc`, `sysfs`, `devtmpfs`, `tmpfs`, `devpts`, `cgroup`, `cgroup2`, `securityfs`, `debugfs`, `tracefs`, `configfs`, `fusectl`, `mqueue`, `hugetlbfs`, `pstore`, `bpf`
- Container filesystems: `overlay`, `overlayfs`, `aufs`, `shiftfs`
- Network filesystems (if not explicitly enabled): `nfs`, `nfs4`, `cifs`, `smb`, `fuse.sshfs`

### Space Metrics (from statfs syscall)

| Field | Type | Formula |
|-------|------|---------|
| `disk_total_bytes` | float64 | `f_blocks * f_bsize` |
| `disk_free_bytes` | float64 | `f_bavail * f_bsize` (available to non-root) |
| `disk_used_bytes` | float64 | `(f_blocks - f_bfree) * f_bsize` |
| `disk_used_percent` | float64 | `disk_used_bytes / (disk_used_bytes + disk_free_bytes) * 100` |
| `disk_free_percent` | float64 | `100 - disk_used_percent` |

**Note:** `disk_used_percent` uses `disk_free_bytes` (available space) not total free space. This excludes reserved blocks and accurately reflects what users can actually use.

### Inode Metrics (from statfs syscall)

| Field | Type | Formula |
|-------|------|---------|
| `inodes_total` | uint64 | `f_files` |
| `inodes_free` | uint64 | `f_ffree` |
| `inodes_used` | uint64 | `f_files - f_ffree` |
| `inodes_used_percent` | float64 | `inodes_used / inodes_total * 100` |

### I/O Utilization Metrics (from /proc/diskstats)

| Field | Type | Formula |
|-------|------|---------|
| `total_utilization_percent` | float64 | `(current_io_time - previous_io_time) / elapsed_ms * 100` (capped at 100%) |
| `read_utilization_percent` | float64 | `total_utilization_percent * (read_time_delta / (read_time_delta + write_time_delta))` |
| `write_utilization_percent` | float64 | `total_utilization_percent * (write_time_delta / (read_time_delta + write_time_delta))` |

Where:
- `io_time`: diskstats field 13 (index 12)
- `read_time`: diskstats field 7 (index 6)
- `write_time`: diskstats field 11 (index 10)
- `elapsed_ms`: time since last collection in milliseconds

### Throughput Metrics (from /proc/diskstats)

| Field | Type | Formula |
|-------|------|---------|
| `read_bytes_per_second` | float64 | `(current_sectors_read - previous_sectors_read) * 512 / elapsed_seconds` |
| `write_bytes_per_second` | float64 | `(current_sectors_written - previous_sectors_written) * 512 / elapsed_seconds` |
| `read_write_bytes_per_second` | float64 | `read_bytes_per_second + write_bytes_per_second` |

Where:
- `sectors_read`: diskstats field 6 (index 5)
- `sectors_written`: diskstats field 10 (index 9)
- Sector size: 512 bytes (Linux standard)

### IOPS Metrics (from /proc/diskstats)

| Field | Type | Formula |
|-------|------|---------|
| `read_io_per_second` | float64 | `(current_reads_completed - previous_reads_completed) / elapsed_seconds` |
| `write_io_per_second` | float64 | `(current_writes_completed - previous_writes_completed) / elapsed_seconds` |

Where:
- `reads_completed`: diskstats field 4 (index 3)
- `writes_completed`: diskstats field 8 (index 7)

---

## Device Mapping Rules

### Device Resolution Strategy

| Device Type | Mount File Pattern | Diskstats Key | Resolution Method |
|-------------|-------------------|---------------|-------------------|
| Regular | `/dev/sda1` | `sda1` | Strip `/dev/` prefix |
| LVM | `/dev/mapper/vg-lv` | `dm-X` | Resolve via major:minor from mountinfo |
| Root pseudo | `/dev/root` | varies | Resolve via `/proc/partitions` using major:minor |

### LVM Device Resolution

1. Parse major:minor from mountinfo (field 3, format `major:minor`)
2. Read `/proc/diskstats` to find matching `dm-X` device
3. Use the `dm-X` name for diskstats lookups

### /dev/root Resolution

1. Get major:minor for `/dev/root` from mountinfo
2. Read `/proc/partitions` to find device name matching major:minor
3. Use resolved device name for diskstats lookups

---

## API Contract

### Endpoint

```
POST /api/v1/agents/{agent_id}/metrics
```

### Headers

```
Content-Type: application/json
```

### Payload Structure

```json
{
  "version": "1.0",
  "timestamp_ms": 1703001234567,
  "resource": {
    "agent_id": "agent-123-abc"
  },
  "metric_sets": [
    {
      "type": "storage",
      "attributes": {
        "mount_point": "/",
        "device": "/dev/sda1",
        "filesystem_type": "ext4",
        "is_read_only": false
      },
      "metrics": {
        "disk_used_bytes": 50000000000.0,
        "disk_free_bytes": 50000000000.0,
        "disk_total_bytes": 100000000000.0,
        "disk_used_percent": 50.0,
        "disk_free_percent": 50.0,
        "total_utilization_percent": 25.5,
        "read_utilization_percent": 15.2,
        "write_utilization_percent": 10.3,
        "read_bytes_per_second": 5242880.0,
        "write_bytes_per_second": 2097152.0,
        "read_write_bytes_per_second": 7340032.0,
        "read_io_per_second": 150.5,
        "write_io_per_second": 80.3,
        "inodes_used": 500000,
        "inodes_free": 9500000,
        "inodes_total": 10000000,
        "inodes_used_percent": 5.0
      }
    },
    {
      "type": "storage",
      "attributes": {
        "mount_point": "/data",
        "device": "/dev/sdb1",
        "filesystem_type": "xfs",
        "is_read_only": false
      },
      "metrics": { ... }
    }
  ]
}
```

### Type Constant

Add to `domain/metrics/metrics.go`:
```go
MetricTypeStorage = "storage"
```

---

## Edge Cases and Error Handling

### Bind Mounts and Shared Devices

Report each mount point as a separate metric set, with shared I/O metrics across mounts of the same device.

**Rationale:**

| Concern | Decision |
|---------|----------|
| Capacity metrics | Unique per mount — users monitor specific paths for disk space alerts (e.g., /var/log at 90%) |
| I/O metrics | Shared across mounts — block-level operations occur at the device layer, not per path |
| Deduplication | Not performed — would lose visibility into individual mount point usage |

**Metric Classification:**

| Category | Metrics | Source | Unique Per Mount? |
|----------|---------|--------|-------------------|
| Identity | mount_point, device, filesystem_type, is_read_only | Mount info | Yes |
| Capacity | disk_used/free/total_bytes, disk_used/free_percent | statfs() | Yes |
| Inodes | inodes_used/free/total, inodes_used_percent | statfs() | Yes |
| Throughput | read/write/read_write_bytes_per_second | /proc/diskstats | No (shared) |
| IOPS | read/write_io_per_second | /proc/diskstats | No (shared) |
| Utilization | total/read/write_utilization_percent | /proc/diskstats | No (shared) |

**Algorithm:**

1. Parse mount info and group mount points by underlying device
2. For each mount point, query statfs() independently (capacity + inodes)
3. For each unique device, read /proc/diskstats and calculate deltas
4. For each mount point: attach capacity metrics (unique) + I/O metrics from parent device (shared)

**Bind Mount Edge Cases:**

| Case | Behavior |
|------|----------|
| Bind mount of subdirectory | Report as separate mount; capacity reflects subdirectory view |
| Read-only remount | Report with is_read_only: true; I/O metrics still shared with writable mount |
| Device unmounted mid-collection | Skip mount point; log warning |
| No previous I/O sample | Return 0.0 for I/O metrics on first collection cycle |

### Mount Disappear/Reappear

A mount can disappear between samples (unmounted, failed, container stopped) and later reappear. Delta calculations must handle stale previous values, counter resets after remount, and time gaps.

**Decision:** Track per-device "last seen" timestamp. Skip delta calculation if device was absent in previous sample.

**Algorithm:**

1. Maintain two maps:
   - `lastStats[device]` → I/O counters
   - `lastSeen[device]` → sample timestamp

2. On each sample, for each device:
   - If device NOT in lastSeen OR lastSeen[device] != previousSampleTime:
     - Device is new or was missing last sample
     - Emit capacity metrics only (no I/O deltas)
   - Else:
     - Device was present in previous sample
     - Calculate deltas normally with counter wrap check
   - Update lastSeen[device] = currentTime
   - Update lastStats[device] = currentCounters

3. Cleanup: Remove stale entries to prevent unbounded memory growth

**State Cleanup:**

Devices that disappear leave orphaned entries in `lastStats` and `lastSeen` maps. Periodic cleanup removes entries not seen for N consecutive samples.

```go
const staleThreshold = 3  // Remove after missing 3 samples

func cleanupStaleEntries(currentDevices map[string]bool) {
    for device := range lastSeen {
        if !currentDevices[device] {
            missedSamples[device]++
            if missedSamples[device] >= staleThreshold {
                delete(lastStats, device)
                delete(lastSeen, device)
                delete(missedSamples, device)
            }
        } else {
            missedSamples[device] = 0
        }
    }
}
```

**Why not immediate removal?**
- Device might be temporarily unavailable (slow NFS mount)
- Brief unmount/remount cycles shouldn't lose state
- 3-sample threshold balances memory vs resilience

**Output Behavior:**

| Scenario | Capacity Metrics | I/O Metrics |
|----------|------------------|-------------|
| Normal sample | ✓ Reported | ✓ Reported |
| First sample ever | ✓ Reported | Return 0.0 |
| Device new this sample | ✓ Reported | Return 0.0 |
| Device missing last sample, back now | ✓ Reported | Return 0.0 |
| Device present consecutively | ✓ Reported | ✓ Reported |
| Counter reset detected | ✓ Reported | Return 0.0 |

**Example Timeline:**

```
T0: /dev/sdb1 at /data     → { capacity: ✓, io: 0.0 (first sample) }
T1: /dev/sdb1 at /data     → { capacity: ✓, io: ✓ }
T2: /data unmounted        → (nothing for /data)
T3: /data remounted        → { capacity: ✓, io: 0.0 (gap detected) }
T4: /dev/sdb1 at /data     → { capacity: ✓, io: ✓ }
```

**Edge Cases:**

| Case | Behavior |
|------|----------|
| Device remounted with different name | Treated as new device |
| Same device, different mount point | Both mounts share I/O stats |
| Very long unmount (hours) | Cleanup removes stale entries; treated as new on return |
| Rapid mount/unmount flapping | Each reappearance returns 0.0 for I/O for one sample |

### Partition vs Whole Disk Stats

`/proc/diskstats` contains entries for both whole disks (sda) and partitions (sda1, sda2). When `/dev/sda1` is mounted, the collector must decide which stats to use.

**Decision:** Always use partition-level stats that match the mounted device.

**Rationale:**

| Concern | Decision |
|---------|----------|
| Accuracy | Partition stats reflect only that partition's I/O |
| Consistency | Matches the capacity metrics which are partition-scoped |
| Whole disk mounted | Rare case; use sda stats (correct for that scenario) |
| Aggregation | Users can sum partitions if they need disk-level totals |

**Mapping Rules:**

| Mount Path Pattern | Diskstats Key | Example |
|--------------------|---------------|---------|
| /dev/sdXN | sdXN | /dev/sda1 → sda1 |
| /dev/sdX (whole disk) | sdX | /dev/sdb → sdb |
| /dev/nvme* | Full name | /dev/nvme0n1p1 → nvme0n1p1 |
| /dev/mapper/* | dm-N | Resolve via major:minor |
| /dev/root | Resolved name | Lookup in /proc/partitions |

**Algorithm:**

```
func getDiskstatsKey(mountDevice):
    if isLVM(mountDevice):
        # /dev/mapper/vg-lv -> dm-X
        majorMinor = lookupMajorMinor(mountDevice)
        return "dm-" + minor

    if isRootDevice(mountDevice):
        # /dev/root -> resolve actual device
        majorMinor = lookupMajorMinor(mountDevice)
        return lookupDeviceName(majorMinor)

    # /dev/sda1 -> sda1, /dev/nvme0n1p1 -> nvme0n1p1
    return strings.TrimPrefix(mountDevice, "/dev/")
```

**Edge Cases:**

| Case | Behavior |
|------|----------|
| Whole disk mounted (/dev/sda) | Use sda stats (unusual but valid) |
| NVMe partition (/dev/nvme0n1p1) | Use nvme0n1p1 stats |
| Software RAID (/dev/md0) | Use md0 stats |
| Loop device (/dev/loop0) | Use loop0 stats (if supported FS) |
| No matching diskstats entry | Return 0.0 for I/O metrics, log warning |

**Example:**

System with `/dev/sda1` at `/` and `/dev/sda2` at `/home`:

```
/proc/diskstats:
   sda   - 1000 reads, 800 writes (aggregate)
   sda1  - 600 reads, 500 writes
   sda2  - 400 reads, 300 writes

Output:
  { mount_point: "/",     device: "/dev/sda1", read_io: <from sda1> }
  { mount_point: "/home", device: "/dev/sda2", read_io: <from sda2> }
```

Note: `sda` aggregate stats are never used unless `/dev/sda` itself is mounted as a filesystem (uncommon).

### Symlink Device Paths

Devices can be mounted using stable symlinks:
- `/dev/disk/by-uuid/<uuid>`
- `/dev/disk/by-label/<label>`
- `/dev/disk/by-id/<id>`
- `/dev/disk/by-path/<path>`

These are symlinks to actual block devices (`/dev/sda1`). Without resolution, device mapping fails and I/O metrics are unavailable.

**Decision:** Resolve symlinks before device mapping. Report original path in output.

**Implementation:**

```go
func resolveDevicePath(devicePath string) string {
    resolved, err := filepath.EvalSymlinks(devicePath)
    if err != nil {
        return devicePath  // Not a symlink or error; use original
    }
    return resolved
}
```

**Resolution Examples:**

| Mount Device | Symlink Target | Diskstats Key |
|--------------|----------------|---------------|
| /dev/disk/by-uuid/abc-123 | /dev/sda1 | sda1 |
| /dev/disk/by-label/DATA | /dev/nvme0n1p2 | nvme0n1p2 |
| /dev/disk/by-id/ata-WD... | /dev/sdb | sdb |
| /dev/sda1 (not symlink) | — | sda1 |

**Output Behavior:**

Report original path (user-facing), use resolved path internally for diskstats lookup:

```json
{
  "mount_point": "/data",
  "device": "/dev/disk/by-uuid/abcd-1234",
  "read_bytes_per_second": 1048576
}
```

**Edge Cases:**

| Case | Behavior |
|------|----------|
| Broken symlink | EvalSymlinks fails; use original path; return 0.0 for I/O |
| Nested symlinks | EvalSymlinks resolves fully |
| Symlink to LVM | Resolved to /dev/dm-X; existing LVM logic handles |
| Symlink to NVMe | Resolved to /dev/nvme0n1p1; standard path handling |

### Btrfs Subvolumes

Btrfs subvolumes present unique challenges:
1. Multiple mount points share one device
2. `statfs()` returns pool-level capacity (not per-subvolume)
3. With quotas, `statfs()` returns quota limits instead
4. No way to get true per-subvolume usage via standard syscalls

**Current Behavior:**

| Feature | Behavior |
|---------|----------|
| Detection | ✓ btrfs in supported filesystems |
| Separate samples | ✓ One per subvolume mount |
| Capacity via statfs | ⚠️ Pool-level or quota-level, not subvolume-level |
| I/O metrics | ✓ Shared (correct — all subvolumes use same device) |
| Per-subvolume usage | ✗ Not available via statfs |

**Capacity Interpretation:**

| Scenario | statfs() Reports | Meaning |
|----------|------------------|---------|
| No quotas | Pool total/free | All subvolumes show same values |
| With qgroup quota | Quota limit as total | Per-subvolume quota, not pool |
| Mixed | Varies per subvolume | Depends on quota assignment |

**Design Decision:** Report statfs as-is. Do not implement btrfs-specific sampling (high complexity, requires root).

**Edge Cases:**

| Case | Behavior |
|------|----------|
| Nested subvolumes | Each mount gets separate sample |
| Subvolume without quota | Reports pool capacity |
| Subvolume with quota | Reports quota as total |
| Snapshot subvolumes | Reported if mounted; capacity shared with source |
| btrfs RAID (multiple devices) | All devices in pool; I/O per-device in diskstats |

**User Guidance:**

- Capacity alerts may trigger on all subvolumes simultaneously (same pool)
- I/O metrics are identical across subvolumes (expected — same device)
- For true per-subvolume usage, use `btrfs filesystem du` outside of agent
- Enable qgroups if per-subvolume quota limits are needed

### Zero Block Size (f_bsize == 0)

The `statfs()` syscall may return `f_bsize == 0`, causing all byte calculations to produce zero and percentage calculations to produce NaN.

**When This Occurs:**

| Scenario | Cause |
|----------|-------|
| Pseudo-filesystem slipped through filter | procfs, sysfs not properly excluded |
| FUSE filesystem bug | Backend returns invalid block size |
| Network filesystem timeout | Stale mount returns zeroed struct |
| Unmounting in progress | Race condition during sample |

**Failure Modes Without Protection:**

| Calculation | Result | Problem |
|-------------|--------|---------|
| total = blocks * 0 | 0 | Looks like empty filesystem |
| used_percent = 0 / (0 + 0) | NaN | Invalid JSON, crashes parsers |

**Algorithm:**

```go
func collectCapacityMetrics(mountPath string) *CapacityMetrics {
    stats := statfs(mountPath)

    // Layer 1: Block size check
    if stats.Bsize == 0 {
        log.Warnf("Invalid block size (0) for %s, skipping capacity", mountPath)
        return nil
    }

    total := uint64(stats.Blocks) * uint64(stats.Bsize)
    free := uint64(stats.Bavail) * uint64(stats.Bsize)
    used := (uint64(stats.Blocks) - uint64(stats.Bfree)) * uint64(stats.Bsize)

    // Layer 2: Zero result check
    if total == 0 && free == 0 && used == 0 {
        log.Warnf("All capacity values zero for %s, skipping", mountPath)
        return nil
    }

    // Layer 3: Safe percentage calculation
    var usedPercent, freePercent float64
    denominator := used + free
    if denominator > 0 {
        usedPercent = float64(used) / float64(denominator) * 100
        freePercent = 100 - usedPercent
    }

    return &CapacityMetrics{...}
}
```

**Defense Layers:**

| Layer | Check | Action |
|-------|-------|--------|
| 1. Filesystem filter | Exclude known pseudo-filesystems | Skip mount entirely |
| 2. Block size check | f_bsize == 0 | Skip capacity metrics |
| 3. Zero result check | total == free == used == 0 | Skip capacity metrics |
| 4. Division guard | denominator == 0 | Return 0.0 for percentages |
| 5. Float validation | isNaN() or isInf() | Return 0.0 |

**Legitimate Empty Filesystem:**

A newly created filesystem may have `f_bavail == f_blocks` (100% free). This is valid:

```
f_bsize = 4096, f_blocks = 1000, f_bfree = 1000
total = 4096000, free = 4096000, used = 0
used_percent = 0%  # Valid — do not filter
```

Only filter when `f_bsize == 0` or all calculated values are zero.

### Device Name Changes

Linux device names are not stable:
- `/dev/sda` can become `/dev/sdb` after reboot (depends on probe order)
- Hot-swap or udev rules can rename devices at runtime
- State keyed by device name becomes invalid after rename

**Decision:** Use in-memory state only. Accept one-sample gap after device name changes.

**Rationale:**

| Approach | Pros | Cons |
|----------|------|------|
| In-memory, name-keyed (chosen) | Simple, no persistence complexity | One-sample gap after rename |
| Persistent state, name-keyed | Survives restart | Stale data after reboot rename |
| Persistent state, major:minor-keyed | Stable across renames | major:minor can also change; adds complexity |
| Persistent state, UUID/serial-keyed | Most stable | Requires additional lookups; not all devices have UUIDs |

**Behavioral Specification:**

| Event | Behavior |
|-------|----------|
| Agent start | Empty state; return 0.0 for I/O on first sample |
| Device name changes (reboot) | Agent restart clears state; treated as fresh start |
| Device name changes (runtime) | Old name orphaned; new name treated as new device; one-sample gap |
| Device disappears | Remains in state until cleanup (see Mount Disappear/Reappear) |

**State Management:**

```
State:
    lastStats: map[deviceName] -> ioCounters  # in-memory only

Lifecycle:
    Agent Start  → lastStats = {}
    Each Sample  → lastStats[device] = currentCounters
    Agent Stop   → lastStats discarded (not persisted)
```

**Why Not Use major:minor for State Key?**

1. major:minor can also change — not guaranteed stable across reboots
2. Adds lookup overhead — must resolve name→major:minor each sample
3. Diminishing returns — reboot already clears state anyway

**Edge Cases:**

| Case | Behavior |
|------|----------|
| sda→sdb after reboot | Agent restarts; fresh state; no issue |
| sda→sdb during runtime | sda orphaned; sdb treated as new; one-sample gap |
| Same physical disk, new partition table | New partition names; treated as new devices |
| LVM rename | dm-X number may change; one-sample gap |
| NVMe namespace change | New name; one-sample gap |

**Example Timeline:**

```
T0: Boot, /dev/sda1 at /
    Agent starts, lastStats = {}
    Output: { device: "/dev/sda1", io_metrics: 0.0 }

T1: Normal sample
    lastStats["sda1"] = { reads: 1000 }
    Output: { device: "/dev/sda1", read_io_per_second: 50 }

-- Reboot, sda1 becomes sdb1 --

T2: Boot, /dev/sdb1 at /
    Agent starts, lastStats = {}
    Output: { device: "/dev/sdb1", io_metrics: 0.0 }

T3: Normal sample
    lastStats["sdb1"] = { reads: 500 }
    Output: { device: "/dev/sdb1", read_io_per_second: 25 }
```

### Clock Adjustments

System clock can change due to NTP synchronization (step or slew), DST transitions, manual adjustment, or VM time sync after suspend/resume. This affects elapsed time calculations.

**Decision:** Use monotonic clock for elapsed time calculation. Return 0.0 for rates if elapsed time is invalid.

**Rationale:**

| Approach | Pros | Cons |
|----------|------|------|
| Wall clock | Simple | Affected by NTP/DST |
| Monotonic clock (chosen) | Immune to adjustments | Not affected by suspend on some systems |
| Hybrid | Best of both | More complex |

**Behavioral Specification:**

| Condition | Behavior |
|-----------|----------|
| elapsed <= 0 | Return 0.0 for all rate metrics |
| elapsed > 2 × sample_interval | Log warning; return 0.0 for rate metrics |
| Normal elapsed | Calculate rates normally |

**Go Implementation Notes:**

`time.Now()` in Go 1.9+ contains both wall clock and monotonic readings. Subtraction uses monotonic automatically:

```go
t1 := time.Now()
// ... NTP adjustment happens ...
t2 := time.Now()
elapsed := t2.Sub(t1)  // Uses monotonic readings, ignores wall clock change
```

**Recommended Protection:**

```go
const (
    maxExpectedInterval = sampleInterval * 2
)

func calculateElapsed(now, last time.Time) time.Duration {
    elapsed := now.Sub(last)  // Uses monotonic in Go 1.9+

    if elapsed <= 0 {
        log.Warn("Negative or zero elapsed time, skipping deltas")
        return 0
    }

    if elapsed > maxExpectedInterval {
        log.Warnf("Unusually large elapsed time: %v, skipping deltas", elapsed)
        return 0
    }

    return elapsed
}
```

**Edge Cases:**

| Case | Behavior |
|------|----------|
| Clock jumps backward (NTP) | elapsed <= 0 → rates = 0.0 |
| Clock jumps forward (NTP) | Large elapsed → skip sample |
| VM suspend/resume | Large elapsed → skip sample |
| Leap second | 1-second error → negligible impact |
| DST (wall clock only) | Using monotonic → no impact |

**Example: VM Suspend/Resume**

```
T0 (mono: 1000): Sample, counters = 5000
T1 (mono: 1010): Sample, counters = 5100, rate = 100/10 = 10/sec
T2 (mono: 5000): VM resumed after suspend, counters = 5200
    - Mono elapsed = 3990 seconds (suspicious!)
    - Skip deltas, return 0.0, log warning
T3 (mono: 5010): Normal sample resumes
```

### Large Filesystem Values

Very large filesystems can cause integer overflow (if 32-bit intermediates are used) or precision loss (when converting to float64).

**Decision:** Use uint64 for all intermediate calculations. Accept float64 precision loss for display (sub-byte accuracy not required).

**Rationale:**

| Approach | Pros | Cons |
|----------|------|------|
| uint64 → float64 (chosen) | Simple; sufficient for monitoring | Precision loss > 8 PB |
| Keep as uint64 | Full precision | JSON numbers may lose precision in parsers |
| Use string representation | Full precision | Complicates downstream processing |
| Use big.Int | Unlimited precision | Overkill; performance overhead |

**Precision Loss by Filesystem Size:**

| Filesystem Size | Behavior |
|-----------------|----------|
| < 8 PB | Exact byte values |
| 8 PB - 16 PB | ±1-2 byte precision loss |
| 16 PB - 16 EB | ±4+ byte precision loss |
| > 16 EB | uint64 overflow (theoretical) |

Sub-byte precision loss is irrelevant for capacity monitoring.

**Implementation Requirements:**

1. All intermediate calculations MUST use uint64 or larger
   - NEVER use int32, uint32, or int for byte counts

2. Multiplication order doesn't matter for uint64
   - `blocks * blockSize` is safe in uint64

3. Final conversion to float64 is acceptable
   - Precision loss at petabyte scale is negligible for monitoring

4. Sector calculations MUST use uint64
   - `sectors * 512` in uint64 space

**Validation for Invalid Values:**

```go
func asValidFloat(value float64) float64 {
    if math.IsNaN(value) || math.IsInf(value, 0) {
        return 0  // Protect against invalid values
    }
    return value
}
```

**Edge Cases:**

| Case | Behavior |
|------|----------|
| Filesystem > 8 PB | Minor precision loss (~bytes) |
| Clustered filesystem > 100 PB | Precision loss (~KB), still usable |
| Counter overflow in diskstats | Kernel wraps at 2^64; return 0.0 for delta |
| Division by zero (0 blocks) | Protected by returning 0.0 for NaN/Inf |

**Example: 10 PB Filesystem**

```
Actual bytes:     11,258,999,068,426,240
uint64:           11,258,999,068,426,240 (exact)
float64:          11,258,999,068,426,240 (exact - within 53-bit mantissa)

Actual bytes:     11,258,999,068,426,241
float64:          11,258,999,068,426,240 (off by 1 byte - acceptable)
```

### Escaped Characters in Mount Paths

Mount points and device paths in `/proc/self/mountinfo` and `/proc/self/mounts` use octal escape sequences for special characters:

| Character | Octal Escape |
|-----------|--------------|
| Space | `\040` |
| Tab | `\011` |
| Newline | `\012` |
| Backslash | `\134` |

**Example:**
```
# Actual mount point: /mnt/My Documents
# In mountinfo:       /mnt/My\040Documents
```

**Handling:** Parse and unescape octal sequences (`\NNN`) in both mount point and device path fields before using them. The unescaped values should be used in the API response.

### First Sample Behavior

Delta-based metrics require two samples. On first collection:

| Metric Category | First Sample Value |
|-----------------|-------------------|
| Utilization (total, read, write) | `0.0` |
| Throughput (bytes/sec) | `0.0` |
| IOPS (io/sec) | `0.0` |
| Space metrics | Actual values (not delta-based) |
| Inode metrics | Actual values (not delta-based) |

Store the first sample and return zeros for delta-based metrics. Second and subsequent collections return calculated values.

### Counter Wraps

Linux counters are 64-bit unsigned integers. Handle wrap-around:

```go
if current < previous {
    // Counter wrapped, use current as new baseline
    delta = 0  // or: delta = current (assume wrap from 0)
}
```

For a 15-second collection interval, 64-bit counters will not wrap under normal conditions.

### Division by Zero

| Scenario | Handling |
|----------|----------|
| `elapsed_seconds == 0` | Return `0.0` for rate metrics |
| `inodes_total == 0` | Return `0.0` for `inodes_used_percent` |
| `read_time_delta + write_time_delta == 0` | Return `0.0` for read/write utilization split |
| `disk_used + disk_free == 0` | Return `0.0` for percent metrics |

### Mount Point Filtering

Skip mount points that:
- Have unsupported filesystem types
- Are virtual filesystems (proc, sysfs, devtmpfs, tmpfs, etc.)
- Fail statfs() call (permission denied, disconnected NFS, etc.)
- Cannot be mapped to a diskstats device

### Device Not Found in Diskstats

If a mount's device cannot be found in `/proc/diskstats`:
- Include the mount point in output
- Set all I/O metrics (utilization, throughput, IOPS) to `0.0`
- Space and inode metrics remain populated from statfs()

### Permission Errors

- statfs() failures: Skip the mount point entirely
- `/proc/diskstats` unreadable: Set all I/O metrics to `0.0`
- `/proc/self/mountinfo` unreadable: Fall back to `/proc/self/mounts`, then `/etc/mtab`

### Utilization Capping

```go
if total_utilization_percent > 100.0 {
    total_utilization_percent = 100.0
}
```

Utilization can exceed 100% due to timing granularity in the kernel. Cap at 100%.

---

## Collector State

The collector must maintain state between collections:

```go
type DiskIOStats struct {
    IOTimeMs       uint64  // field 13
    ReadTimeMs     uint64  // field 7
    WriteTimeMs    uint64  // field 11
    SectorsRead    uint64  // field 6
    SectorsWritten uint64  // field 10
    ReadsCompleted uint64  // field 4
    WritesCompleted uint64 // field 8
}

type collector struct {
    lastDiskStats map[string]DiskIOStats  // key: device name
    lastTime      time.Time
}
```

---

## Implementation Patterns

Follow existing collector patterns in the codebase:

1. **Interface abstraction** for testability (see `sysmetrics.SystemCollector`)
2. **Config struct** for dependency injection
3. **Constructor pattern**: `New()` and `NewWithConfig(cfg *Config)`
4. **Delta calculation** with elapsed time (see `networkmetrics.Collect`)
5. **Graceful degradation** - don't fail entire collection for partial errors

### Package Location

```
internal/storagemetrics/
├── collector.go       # Main collector implementation
├── collector_test.go  # Unit tests with mock interfaces
├── mountinfo.go       # Mount parsing logic
├── diskstats.go       # Diskstats parsing logic
└── doc.go             # Package documentation
```

### Domain Types Location

Add to `domain/metrics/metrics.go`:

```go
type StorageMetrics struct {
    DiskUsedBytes           float64 `json:"disk_used_bytes"`
    DiskFreeBytes           float64 `json:"disk_free_bytes"`
    DiskTotalBytes          float64 `json:"disk_total_bytes"`
    DiskUsedPercent         float64 `json:"disk_used_percent"`
    DiskFreePercent         float64 `json:"disk_free_percent"`
    TotalUtilizationPercent float64 `json:"total_utilization_percent"`
    ReadUtilizationPercent  float64 `json:"read_utilization_percent"`
    WriteUtilizationPercent float64 `json:"write_utilization_percent"`
    ReadBytesPerSecond      float64 `json:"read_bytes_per_second"`
    WriteBytesPerSecond     float64 `json:"write_bytes_per_second"`
    ReadWriteBytesPerSecond float64 `json:"read_write_bytes_per_second"`
    ReadIOPerSecond         float64 `json:"read_io_per_second"`
    WriteIOPerSecond        float64 `json:"write_io_per_second"`
    InodesUsed              uint64  `json:"inodes_used"`
    InodesFree              uint64  `json:"inodes_free"`
    InodesTotal             uint64  `json:"inodes_total"`
    InodesUsedPercent       float64 `json:"inodes_used_percent"`
}

type StorageAttributes struct {
    MountPoint     string `json:"mount_point"`
    Device         string `json:"device"`
    FilesystemType string `json:"filesystem_type"`
    IsReadOnly     bool   `json:"is_read_only"`
}
```
