package source

import "os"

// os.Root rejects reserved device names on Windows; Open checks regular mode.
func openSource(root *os.Root, name string) (*os.File, error) {
	if root == nil {
		return os.Open(name)
	}
	return root.Open(name)
}
