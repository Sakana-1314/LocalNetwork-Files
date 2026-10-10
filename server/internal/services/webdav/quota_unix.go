//go:build unix

package webdav

import "syscall"

// diskUsage reports the free and used bytes of the filesystems holding paths,
// counting each filesystem once.
func diskUsage(paths ...string) (avail, used uint64, ok bool) {
	seen := map[syscall.Fsid]bool{}
	for _, p := range paths {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil || seen[st.Fsid] {
			continue
		}
		seen[st.Fsid] = true
		bs := uint64(st.Bsize)
		avail += uint64(st.Bavail) * bs
		used += (uint64(st.Blocks) - uint64(st.Bfree)) * bs
		ok = true
	}
	return avail, used, ok
}
