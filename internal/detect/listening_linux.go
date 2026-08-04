//go:build linux

package detect

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Listening reads listening sockets from /proc; a pid it cannot read is skipped, not an error.
func (procSource) Listening() ([]Listener, error) {
	owners := socketOwners()

	var out []Listener
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		for _, sock := range listeningSockets(path) {
			pid, ok := owners[sock.inode]
			if !ok {
				continue
			}
			cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "cwd"))
			if err != nil {
				continue
			}
			out = append(out, Listener{Port: sock.port, PID: pid, Cwd: cwd})
		}
	}
	return out, nil
}

type listeningSocket struct {
	port  int
	inode uint64
}

const tcpListenState = "0A" // TCP_LISTEN in the /proc/net/tcp state column

func listeningSockets(path string) []listeningSocket {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var out []listeningSocket
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[3] != tcpListenState {
			continue
		}
		_, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseInt(hexPort, 16, 32)
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, listeningSocket{port: int(port), inode: inode})
	}
	return out
}

func socketOwners() map[uint64]int {
	owners := map[uint64]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return owners
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inodeStr, ok := strings.CutPrefix(link, "socket:[")
			if !ok {
				continue
			}
			inodeStr = strings.TrimSuffix(inodeStr, "]")
			inode, err := strconv.ParseUint(inodeStr, 10, 64)
			if err != nil {
				continue
			}
			owners[inode] = pid
		}
	}
	return owners
}
