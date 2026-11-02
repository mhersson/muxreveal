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

	pages, err := readVMPages()
	if err != nil {
		return loadSample{}, err
	}

	page, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return loadSample{}, fmt.Errorf("sysctl hw.pagesize: %w", err)
	}

	return loadSample{CPU: cpu, Mem: pages.used(total, uint64(page))}, nil
}

func readVMPages() (vmPages, error) {
	var p vmPages

	for _, c := range []struct {
		name string
		dst  *uint64
	}{
		{"vm.page_free_count", &p.free},
		{"vm.page_pageable_external_count", &p.external},
		{"vm.page_purgeable_count", &p.purgeable},
	} {
		raw, err := unix.SysctlRaw(c.name)
		if err != nil {
			return vmPages{}, fmt.Errorf("sysctl %s: %w", c.name, err)
		}

		if *c.dst, err = vmCount(c.name, raw); err != nil {
			return vmPages{}, err
		}
	}

	return p, nil
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
