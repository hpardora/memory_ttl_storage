//go:build !unix

package keepcache

import "errors"

func mkfifo(string) error { return errors.New("fifo not supported") }
