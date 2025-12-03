package sysmetrics

import (
	"context"
	"errors"
	"fmt"
	"hostlink/domain/metrics"
	"strconv"
	"strings"
)

const (
	cpuCommand     = "top -bn1 | grep 'Cpu(s)' | sed 's/,/ /g' | awk '{print 100 - $8}'"
	memoryCommand  = "free -m | grep Mem | awk '{printf \"%.2f\", $3/$2*100}'"
	loadAvgCommand = "cat /proc/loadavg | awk '{print $1, $2, $3}'"
	swapCommand    = "free -m | grep Swap | awk '{if($2>0) printf \"%.2f\", $3/$2*100; else print 0}'"
)

type CommandExecutor interface {
	Execute(ctx context.Context, command string) (string, error)
}

type Collector interface {
	Collect(ctx context.Context) (metrics.SystemMetrics, error)
}

type Config struct {
	DiskPath string
}

type collector struct {
	executor CommandExecutor
	diskPath string
}

func New(executor CommandExecutor, cfg Config) Collector {
	return &collector{
		executor: executor,
		diskPath: cfg.DiskPath,
	}
}

func (c *collector) Collect(ctx context.Context) (metrics.SystemMetrics, error) {
	var m metrics.SystemMetrics
	var errs []error

	cpu, err := c.collectCPU(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("cpu: %w", err))
	} else {
		m.CPUPercent = cpu
	}

	mem, err := c.collectMemory(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("memory: %w", err))
	} else {
		m.MemoryPercent = mem
	}

	l1, l5, l15, err := c.collectLoadAvg(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("loadavg: %w", err))
	} else {
		m.LoadAvg1 = l1
		m.LoadAvg5 = l5
		m.LoadAvg15 = l15
	}

	swap, err := c.collectSwap(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("swap: %w", err))
	} else {
		m.SwapUsagePercent = swap
	}

	disk, err := c.collectDisk(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("disk: %w", err))
	} else {
		m.DiskUsagePercent = disk
	}

	if len(errs) > 0 {
		return m, errors.Join(errs...)
	}
	return m, nil
}

func (c *collector) collectCPU(ctx context.Context) (float64, error) {
	output, err := c.executor.Execute(ctx, cpuCommand)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(output), 64)
}

func (c *collector) collectMemory(ctx context.Context) (float64, error) {
	output, err := c.executor.Execute(ctx, memoryCommand)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(output), 64)
}

func (c *collector) collectLoadAvg(ctx context.Context) (float64, float64, float64, error) {
	output, err := c.executor.Execute(ctx, loadAvgCommand)
	if err != nil {
		return 0, 0, 0, err
	}

	parts := strings.Fields(strings.TrimSpace(output))
	if len(parts) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected loadavg format: %s", output)
	}

	l1, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse loadavg1: %w", err)
	}

	l5, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse loadavg5: %w", err)
	}

	l15, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse loadavg15: %w", err)
	}

	return l1, l5, l15, nil
}

func (c *collector) collectSwap(ctx context.Context) (float64, error) {
	output, err := c.executor.Execute(ctx, swapCommand)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(output), 64)
}

func (c *collector) collectDisk(ctx context.Context) (float64, error) {
	output, err := c.executor.Execute(ctx, c.diskCommand())
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(output), 64)
}

func (c *collector) diskCommand() string {
	return fmt.Sprintf("df %s | tail -1 | awk '{print $5}' | tr -d '%%'", c.diskPath)
}
