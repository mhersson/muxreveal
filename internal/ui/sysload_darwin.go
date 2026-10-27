package ui

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// loadavgSize is the size of the kernel's struct loadavg: three uint32
// averages, 4 bytes of padding and an int64 scale.
const loadavgSize = 24

// sysLoad samples sysctl. Darwin has no cheap per-core tick counters through
// sysctl, so the CPU share is the one-minute load average over the cores.
type sysLoad struct{}

func newSysLoad() loadSampler { return sysLoad{} }

func (sysLoad) Sample(context.Context) (loadSample, error) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return loadSample{}, fmt.Errorf("sysctl vm.loadavg: %w", err)
	}

	cpu, err := loadShare(raw, runtime.NumCPU())
	if err != nil {
		return loadSample{}, err
	}

	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return loadSample{}, fmt.Errorf("sysctl hw.memsize: %w", err)
	}

	free, err := unix.SysctlUint32("vm.page_free_count")
	if err != nil {
		return loadSample{}, fmt.Errorf("sysctl vm.page_free_count: %w", err)
	}

	page, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return loadSample{}, fmt.Errorf("sysctl hw.pagesize: %w", err)
	}

	freeBytes := min(uint64(free)*uint64(page), total)

	return loadSample{CPU: cpu, Mem: total - freeBytes}, nil
}

// loadShare turns a struct loadavg into a percentage of cores, at most 100.
func loadShare(raw []byte, cores int) (int, error) {
	if len(raw) < loadavgSize || cores < 1 {
		return 0, errors.New("sysctl vm.loadavg: short reply")
	}

	scale := binary.NativeEndian.Uint64(raw[16:24])
	if scale == 0 {
		return 0, errors.New("sysctl vm.loadavg: zero scale")
	}

	one := float64(binary.NativeEndian.Uint32(raw[0:4])) / float64(scale)

	return min(int(one*100/float64(cores)+0.5), 100), nil
}
