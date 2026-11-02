package term

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"strings"
)

// procargsName names a process from its kern.procargs2 buffer (argc as a
// uint32, the exec path, NUL padding, then argv): the base name of argv[0],
// without a login shell's leading "-", which Linux's comm does not have either.
// An empty argv[0] cannot be told from the padding, as in ps.
//
// For node or bun it is argv[1]'s base name, unless that is an option: an npm
// install's "#!/usr/bin/env node" makes claude's argv ["node", "<path>/claude"].
func procargsName(b []byte) (string, bool) {
	if len(b) < 4 {
		return "", false
	}

	argc := binary.NativeEndian.Uint32(b)
	if argc == 0 {
		return "", false
	}

	b = b[4:]

	end := bytes.IndexByte(b, 0)
	if end < 0 {
		return "", false
	}

	arg0, rest, ok := bytes.Cut(bytes.TrimLeft(b[end:], "\x00"), []byte{0})
	if !ok {
		return "", false
	}

	name, ok := baseName(strings.TrimPrefix(string(arg0), "-"))
	if !ok {
		return "", false
	}

	if (name == "node" || name == "bun") && argc > 1 {
		arg1, _, ok := bytes.Cut(rest, []byte{0})
		if ok && !bytes.HasPrefix(arg1, []byte("-")) {
			if script, ok := baseName(string(arg1)); ok {
				return script, true
			}
		}
	}

	return name, true
}

// baseName is the base name of path, false for an empty path or a root.
func baseName(path string) (string, bool) {
	name := filepath.Base(path)

	return name, name != "." && name != "/"
}
