//go:build !unix

package webdav

func diskUsage(...string) (avail, used uint64, ok bool) { return 0, 0, false }
