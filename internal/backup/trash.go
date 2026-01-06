package backup

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// MoveToTrash 将文件移动到系统回收站（最佳努力）
// macOS: ~/.Trash
// Linux: XDG 规范 ~/.local/share/Trash/{files,info}
// Windows: Shell API（SHFileOperationW）移动到系统回收站
func MoveToTrash(files []string) (trashed []string, failed map[string]string) {
	failed = make(map[string]string)
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			for _, f := range files {
				failed[f] = err.Error()
			}
			return
		}
		trash := filepath.Join(home, ".Trash")
		_ = os.MkdirAll(trash, 0o755)
		for _, src := range files {
			base := filepath.Base(src)
			dst := filepath.Join(trash, fmt.Sprintf("%s.%d", base, time.Now().UnixNano()))
			if err := os.Rename(src, dst); err != nil {
				if cerr := copyFile(src, dst); cerr != nil {
					failed[src] = cerr.Error()
					continue
				}
				if derr := os.Remove(src); derr != nil {
					failed[src] = derr.Error()
					continue
				}
			}
			trashed = append(trashed, dst)
		}
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			for _, f := range files {
				failed[f] = err.Error()
			}
			return
		}
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, ".local", "share")
		}
		trashFiles := filepath.Join(base, "Trash", "files")
		trashInfo := filepath.Join(base, "Trash", "info")
		_ = os.MkdirAll(trashFiles, 0o755)
		_ = os.MkdirAll(trashInfo, 0o755)
		for _, src := range files {
			base := filepath.Base(src)
			unique := fmt.Sprintf("%s.%d", base, time.Now().UnixNano())
			dst := filepath.Join(trashFiles, unique)
			if err := os.Rename(src, dst); err != nil {
				if cerr := copyFile(src, dst); cerr != nil {
					failed[src] = cerr.Error()
					continue
				}
				if derr := os.Remove(src); derr != nil {
					failed[src] = derr.Error()
					continue
				}
			}
			trashed = append(trashed, dst)
			_ = writeTrashInfo(filepath.Join(trashInfo, unique+".trashinfo"), src)
		}
	case "windows":
		for _, src := range files {
			if err := recycleToWindowsTrash(src); err != nil {
				failed[src] = err.Error()
				continue
			}
			trashed = append(trashed, src)
		}
	default:
		for _, src := range files {
			if err := os.Remove(src); err != nil {
				failed[src] = err.Error()
				continue
			}
			trashed = append(trashed, src)
		}
	}
	return
}

func writeTrashInfo(infoPath, original string) error {
	_ = os.MkdirAll(filepath.Dir(infoPath), 0o755)
	// freedesktop TrashInfo 格式
	enc := url.PathEscape(original)
	// 使用本地时间，格式 YYYY-MM-DDThh:mm:ss
	ts := time.Now().Format("2006-01-02T15:04:05")
	content := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", enc, ts)
	return os.WriteFile(infoPath, []byte(content), 0o644)
}
