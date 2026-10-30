package service

import (
	"os"

	"golang.org/x/sys/windows"
)

// Downloads keep their original descriptor when the writer rotates its name.
func openGenerationLogRead(filename string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(filename)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: filename, Err: err}
	}
	return os.NewFile(uintptr(handle), filename), nil
}
