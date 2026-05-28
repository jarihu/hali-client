//go:build windows

package cmd

import (
	"math"

	"golang.org/x/sys/windows"
)

func availableDiskBytes(path string) (int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeBytesAvailable uint64
	var totalNumberOfBytes uint64
	var totalNumberOfFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeBytesAvailable, &totalNumberOfBytes, &totalNumberOfFreeBytes); err != nil {
		return 0, err
	}
	if freeBytesAvailable > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(freeBytesAvailable), nil
}
