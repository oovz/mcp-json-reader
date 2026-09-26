package source

import "os"

// os.Root rejects reserved device names on Windows; Open checks regular mode.
func openConfined(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
