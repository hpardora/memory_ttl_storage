//go:build unix

package memory_ttl_storage

import "syscall"

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o600) }
