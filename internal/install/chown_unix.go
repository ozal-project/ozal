//go:build unix

package install

import (
	"os"
	"syscall"
)

// chownLike gives f the owner and group of info.
func chownLike(f *os.File, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) == os.Geteuid() && int(st.Gid) == os.Getegid() {
		return nil
	}
	return f.Chown(int(st.Uid), int(st.Gid))
}
