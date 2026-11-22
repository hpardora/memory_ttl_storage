//go:build !unix

package memory_ttl_storage

import "errors"

func mkfifo(string) error { return errors.New("fifo not supported") }
