package backup

import "os"

// DeletePermanently 直接永久删除文件（不可恢复）
// 返回成功删除的文件列表与失败映射
func DeletePermanently(files []string) (deleted []string, failed map[string]string) {
	failed = make(map[string]string)
	for _, src := range files {
		if err := os.Remove(src); err != nil {
			failed[src] = err.Error()
			continue
		}
		deleted = append(deleted, src)
	}
	return
}
