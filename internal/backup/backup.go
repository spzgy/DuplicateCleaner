package backup

import (
	"fmt"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// RestoreMapping 描述一次删除备份的映射关系
type RestoreMapping struct {
	Source string `json:"source"`
	Backup string `json:"backup"`
}

// EnsureDir 确保备份目录存在
func EnsureDir(backupDir string) error {
	if backupDir == "" {
		return fmt.Errorf("未配置备份目录")
	}
	return os.MkdirAll(backupDir, 0o755)
}

// MoveToBackup 将待删除文件移动到备份目录（不做永久删除）
// 备份文件命名包含时间戳，避免覆盖
func MoveToBackup(files []string, backupDir string) (moved []string, failed map[string]string) {
	_ = EnsureDir(backupDir)
	failed = make(map[string]string)
	for _, src := range files {
		base := filepath.Base(src)
		dst := filepath.Join(backupDir, fmt.Sprintf("%s.%d.bak", base, time.Now().UnixNano()))
		// 尝试重命名（跨盘可能失败）
		if err := os.Rename(src, dst); err != nil {
			// 回退为复制
			if cerr := copyFile(src, dst); cerr != nil {
				failed[src] = cerr.Error()
				continue
			}
			// 复制成功后删除源文件
			if derr := os.Remove(src); derr != nil {
				failed[src] = derr.Error()
				continue
			}
		}
		moved = append(moved, dst)
	}
	return moved, failed
}

// MoveToBackupWithManifest 将文件移动到备份，并写入可用于恢复的清单 restore_last.json
func MoveToBackupWithManifest(files []string, backupDir string) (moved []string, failed map[string]string, manifest string) {
	moved, failed = MoveToBackup(files, backupDir)
	var mappings []RestoreMapping
	for i := range files {
		if i < len(moved) {
			mappings = append(mappings, RestoreMapping{Source: files[i], Backup: moved[i]})
		}
	}
	manifest = filepath.Join(backupDir, "restore_last.json")
	_ = writeJSONFile(manifest, mappings)
	return moved, failed, manifest
}

// RestoreLast 从 restore_last.json 恢复上一次删除的文件（不覆盖已存在的原文件）
func RestoreLast(backupDir string) (restored []string, failed map[string]string) {
	failed = map[string]string{}
	manifest := filepath.Join(backupDir, "restore_last.json")
	b, err := os.ReadFile(manifest)
	if err != nil {
		failed[manifest] = err.Error()
		return
	}
	var mappings []RestoreMapping
	if err := json.Unmarshal(b, &mappings); err != nil {
		failed[manifest] = err.Error()
		return
	}
	for _, m := range mappings {
		// 原路径已存在则跳过，避免覆盖
		if _, err := os.Stat(m.Source); err == nil {
			failed[m.Source] = "目标已存在，跳过恢复"
			continue
		}
		// 确保目标目录存在
		_ = os.MkdirAll(filepath.Dir(m.Source), 0o755)
		// 尝试重命名回原路径
		if err := os.Rename(m.Backup, m.Source); err != nil {
			// 回退为复制
			if cerr := copyFile(m.Backup, m.Source); cerr != nil {
				failed[m.Source] = cerr.Error()
				continue
			}
			// 复制成功后删除备份文件
			if derr := os.Remove(m.Backup); derr != nil {
				failed[m.Source] = derr.Error()
				continue
			}
		}
		restored = append(restored, m.Source)
	}
	return
}

func writeJSONFile(path string, v any) error {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
