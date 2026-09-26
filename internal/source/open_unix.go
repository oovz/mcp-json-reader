//go:build unix

package source

import (
	"os"
	"syscall"
)

// O_NONBLOCK prevents a confined FIFO from blocking before File.Stat can reject
// it. The opened descriptor is always checked for regular-file mode by Open.
func openConfined(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
