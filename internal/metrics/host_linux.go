//go:build linux

package metrics

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type DefaultHostReader struct{}

func (DefaultHostReader) ReadHost(ctx context.Context) (HostSnapshot, error) {
	first, err := readCPU()
	if err != nil {
		return HostSnapshot{}, err
	}
	select {
	case <-ctx.Done():
		return HostSnapshot{}, ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	second, err := readCPU()
	if err != nil {
		return HostSnapshot{}, err
	}
	cpu := cpuPercent(first, second)
	total, used, err := readMemory()
	if err != nil {
		return HostSnapshot{}, err
	}
	diskTotal, diskUsed, err := readDisk("/")
	if err != nil {
		return HostSnapshot{}, err
	}
	return HostSnapshot{
		CPUPercent:       &cpu,
		MemoryTotalBytes: &total,
		MemoryUsedBytes:  &used,
		DiskTotalBytes:   &diskTotal,
		DiskUsedBytes:    &diskUsed,
	}, nil
}

type cpuTimes struct {
	idle  uint64
	total uint64
}

func readCPU() (cpuTimes, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, fmt.Errorf("read /proc/stat: %w", err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimes{}, fmt.Errorf("parse /proc/stat cpu line")
	}
	var times []uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("parse /proc/stat value: %w", err)
		}
		times = append(times, value)
	}
	idle := times[3]
	if len(times) > 4 {
		idle += times[4]
	}
	var total uint64
	for _, value := range times {
		total += value
	}
	return cpuTimes{idle: idle, total: total}, nil
}

func cpuPercent(first, second cpuTimes) float64 {
	total := second.total - first.total
	idle := second.idle - first.idle
	if total == 0 || idle > total {
		return 0
	}
	return float64(total-idle) * 100 / float64(total)
}

func readMemory() (uint64, uint64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	defer file.Close()

	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse /proc/meminfo: %w", err)
		}
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan /proc/meminfo: %w", err)
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total == 0 {
		return 0, 0, fmt.Errorf("parse /proc/meminfo: missing MemTotal")
	}
	if available > total {
		available = total
	}
	return total, total - available, nil
}

func readDisk(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if free > total {
		free = total
	}
	return total, total - free, nil
}
