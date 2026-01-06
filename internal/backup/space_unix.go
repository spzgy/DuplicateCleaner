//go:build !windows

package backup

import (
	"os"
	"path/filepath"
	"syscall"
)

// SameVolume 判断源路径与目标目录是否在同一文件系统卷
func SameVolume(src, dstDir string) (bool, error) {
	si, err := os.Stat(src)
	if err != nil {
		return false, err
	}
	di, err := os.Stat(filepath.Clean(dstDir))
	if err != nil {
		return false, err
	}
	ss, ok1 := si.Sys().(*syscall.Stat_t)
	ds, ok2 := di.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false, nil
	}
	return ss.Dev == ds.Dev, nil
}

// FreeSpace 返回目标目录所在文件系统的可用空间（字节）
func FreeSpace(path string) (uint64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(filepath.Clean(path), &fs); err != nil {
		return 0, err
	}
	return uint64(fs.Bavail) * uint64(fs.Bsize), nil
}

// RequiredSpaceForCopy 计算拷贝到目标目录所需的总空间（针对跨卷的文件）
func RequiredSpaceForCopy(files []string, dstDir string) (uint64, error) {
	var total uint64
	for _, f := range files {
		same, err := SameVolume(f, dstDir)
		if err != nil {
			return 0, err
		}
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

// TrashTargetDir 返回当前平台的回收站目标目录及是否需要空间检测
// macOS 与 Linux 使用用户目录下回收站，需要空间检测；Windows 由系统在同卷回收，不需要检测
func TrashTargetDir() (string, bool) {
	home, _ := os.UserHomeDir()
	if home == "" {
		return "", true
	}
	// macOS: ~/.Trash
	if isDarwin() {
		return filepath.Join(home, ".Trash"), true
	}
	// Linux: ~/.local/share/Trash/files
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "Trash", "files"), true
}

func isDarwin() bool {
	return os.Getenv("GOOS") == "darwin"
}
