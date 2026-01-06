//go:build windows

package backup

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// SameVolume 判断源路径与目标目录是否在同一卷（通过驱动器号/卷名近似判断）
func SameVolume(src, dstDir string) (bool, error) {
	return filepath.VolumeName(src) == filepath.VolumeName(dstDir), nil
}

// FreeSpace 返回目标目录所在卷的可用空间（字节）
func FreeSpace(path string) (uint64, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	var freeAvailable uint64
	var totalBytes uint64
	var totalFree uint64
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	r1, _, callErr := proc.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeAvailable)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)))
	if callErr != syscall.Errno(0) && callErr != nil {
		return 0, callErr
	}
	if r1 == 0 {
		return 0, syscall.Errno(r1)
	}
	return freeAvailable, nil
}

// RequiredSpaceForCopy 计算拷贝到目标目录所需的总空间（针对跨卷的文件）
func RequiredSpaceForCopy(files []string, dstDir string) (uint64, error) {
	var total uint64
	for _, f := range files {
		same, _ := SameVolume(f, dstDir)
		if same {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil {
			return 0, err
		}
		total += uint64(fi.Size())
	}
	return total, nil
}

// TrashTargetDir 返回回收站目标目录及是否需要空间检测
// Windows 使用系统回收站（同卷移动），无需额外空间检测
func TrashTargetDir() (string, bool) {
	return "", false
}
