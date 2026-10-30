//go:build !windows

package service

import "os"

func openGenerationLogRead(filename string) (*os.File, error) {
	return os.Open(filename)
}
