//go:build unix

package source

import (
	"os"
	"syscall"
)

// O_NONBLOCK prevents a FIFO from blocking before File.Stat can reject
// it. The opened descriptor is always checked for regular-file mode by Open.
func openSource(root *os.Root, name string) (*os.File, error) {
	if root == nil {
		return os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	}
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
