package ui

import (
	"encoding/binary"
	"testing"
)

func TestLoadShare(t *testing.T) {
	loadavg := func(one uint32, scale uint64) []byte {
		b := make([]byte, loadavgSize)
		binary.NativeEndian.PutUint32(b[0:4], one)
		binary.NativeEndian.PutUint64(b[16:24], scale)

		return b
	}

	tests := []struct {
		name  string
		raw   []byte
		cores int
		want  int
		fail  bool
	}{
		{"idle", loadavg(0, 2048), 8, 0, false},
		{"half", loadavg(4*2048, 2048), 8, 50, false},
		{"capped", loadavg(20*2048, 2048), 8, 100, false},
		{"short reply", make([]byte, 8), 8, 0, true},
		{"zero scale", loadavg(1, 0), 8, 0, true},
		{"no cores", loadavg(1, 2048), 0, 0, true},
	}

	for _, tt := range tests {
		got, err := loadShare(tt.raw, tt.cores)
		if (err != nil) != tt.fail || got != tt.want {
			t.Errorf("%s: loadShare = %d, %v, want %d (fail %v)", tt.name, got, err, tt.want, tt.fail)
		}
	}
}
