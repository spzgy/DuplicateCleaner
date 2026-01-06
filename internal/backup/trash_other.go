//go:build !windows

package backup

import "fmt"

// recycleToWindowsTrash 非 Windows 平台的占位实现
func recycleToWindowsTrash(path string) error {
	return fmt.Errorf("windows recycle bin unsupported on this platform")
}
