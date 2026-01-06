package backup

import (
	"os"
	"path/filepath"
	"strings"
)

func CleanEmptyDirs(fromPaths []string, boundaries []string) (removed []string, failed map[string]string) {
	failed = make(map[string]string)
	removed = make([]string, 0)
	var bounds []string
	for _, b := range boundaries {
		if b == "" {
			continue
		}
		bounds = append(bounds, filepath.Clean(b))
	}
	seen := map[string]struct{}{}
	for _, p := range fromPaths {
		dir := filepath.Dir(p)
		for {
			if _, ok := seen[dir]; ok {
				break
			}
			if !withinBounds(dir, bounds) {
				break
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				failed[dir] = err.Error()
				break
			}
			if len(entries) > 0 {
				break
			}
			if err := os.Remove(dir); err != nil {
				failed[dir] = err.Error()
				break
			}
			removed = append(removed, dir)
			seen[dir] = struct{}{}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return
}

func withinBounds(path string, bounds []string) bool {
	p := filepath.Clean(path)
	for _, b := range bounds {
		if p == b {
			return true
		}
		if strings.HasPrefix(p, b+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
