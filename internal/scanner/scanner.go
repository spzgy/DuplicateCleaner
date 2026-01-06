package scanner

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"duplicatecleaner/internal/cache"
	"duplicatecleaner/internal/hash"
	"duplicatecleaner/internal/types"
)

// Scanner 提供目录扫描与重复文件识别
type Scanner struct {
	opts types.ScanOptions
}

// NewScanner 创建新扫描器
func NewScanner(opts types.ScanOptions) *Scanner {
	if opts.HashAlgo == "" {
		opts.HashAlgo = "sha256"
	}
	if opts.ChunkSizeBytes == 0 {
		opts.ChunkSizeBytes = 4 * 1024 * 1024
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = runtime.NumCPU()
	}
	return &Scanner{opts: opts}
}

// Scan 扫描指定目录并返回重复文件分组
// 1) 先用大小分桶，减少哈希计算
// 2) 对可能重复的桶计算哈希并分组
func (s *Scanner) Scan() ([]types.DuplicateGroup, []types.FileMeta, error) {
	if len(s.opts.Dirs) == 0 {
		return nil, nil, errors.New("未指定扫描目录")
	}
	// 缓存
	var c cache.Store
	if s.opts.EnableHashCache {
		c.Load(s.opts.HashCachePath)
	}
	sizeBuckets := map[int64][]types.FileMeta{}
	var allFiles []types.FileMeta
	visited := map[string]struct{}{}

	for _, root := range s.opts.Dirs {
		root = filepath.Clean(root)
		if s.opts.SkipSystemDirs && isSystemDir(root) {
			continue
		}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				allFiles = append(allFiles, types.FileMeta{Path: p, AccessError: err.Error()})
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if s.opts.SkipHidden && isHidden(name) {
					return filepath.SkipDir
				}
				if s.opts.SkipSystemDirs && isSystemDir(p) {
					return filepath.SkipDir
				}
				return nil
			}
			info, statErr := d.Info()
			if statErr != nil {
				allFiles = append(allFiles, types.FileMeta{Path: p, AccessError: statErr.Error()})
				return nil
			}
			base := filepath.Base(p)
			if s.opts.SkipHidden && isHidden(base) {
				return nil
			}
			if s.opts.SkipSystemFiles && isSystemFile(base) {
				return nil
			}
			if s.opts.SkipReadOnly {
				if !isWritable(p) {
					return nil
				}
			}
			isSym := isSymlink(d)
			if isSym && !s.opts.FollowSymlinks {
				return nil
			}
			if _, seen := visited[p]; seen {
				return nil
			}
			visited[p] = struct{}{}

			meta := types.FileMeta{
				Path:      p,
				Size:      info.Size(),
				ModTime:   info.ModTime(),
				IsSymlink: isSym,
			}
			allFiles = append(allFiles, meta)
			// 大小分桶
			if info.Size() > 0 {
				sizeBuckets[info.Size()] = append(sizeBuckets[info.Size()], meta)
			}
			return nil
		})
		if err != nil {
			return nil, allFiles, err
		}
	}

	// 对桶中元素>1的进行哈希并分组
	type pair struct {
		hash string
		meta types.FileMeta
	}
	var mu sync.Mutex
	hashGroups := map[string][]types.FileMeta{}

	worker := func(files []types.FileMeta, wg *sync.WaitGroup) {
		defer wg.Done()
		for _, fm := range files {
			// 再次校验未变更
			beforeInfo, _ := os.Stat(fm.Path)
			var hv string
			var err error
			// 命中缓存直接复用
			if s.opts.EnableHashCache && beforeInfo != nil {
				if cached, ok := c.Get(fm.Path, beforeInfo.Size(), beforeInfo.ModTime(), s.opts.HashAlgo); ok {
					hv = cached
				}
			}
			if hv == "" {
				hv, err = hash.ComputeHash(fm.Path, s.opts.HashAlgo, s.opts.ChunkSizeBytes)
			}
			afterInfo, _ := os.Stat(fm.Path)
			if err != nil {
				fm.AccessError = err.Error()
			} else {
				// 检测哈希过程中文件是否被修改
				if beforeInfo != nil && afterInfo != nil {
					if beforeInfo.ModTime() != afterInfo.ModTime() || beforeInfo.Size() != afterInfo.Size() {
						fm.AccessError = "文件在哈希计算过程中发生变化"
					}
				}
				fm.HashAlgo = s.opts.HashAlgo
				fm.HashValue = hv
				if s.opts.EnableHashCache && afterInfo != nil {
					c.Put(fm.Path, afterInfo.Size(), afterInfo.ModTime(), s.opts.HashAlgo, hv)
				}
			}
			mu.Lock()
			hashGroups[hv] = append(hashGroups[hv], fm)
			mu.Unlock()
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, s.opts.MaxWorkers)
	for _, bucket := range sizeBuckets {
		if len(bucket) < 2 {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(files []types.FileMeta) {
			defer func() { <-sem }()
			worker(files, &wg)
		}(bucket)
	}
	wg.Wait()

	// 构建重复分组
	var groups []types.DuplicateGroup
	for h, files := range hashGroups {
		// 过滤只有一个文件的哈希
		if len(files) < 2 {
			continue
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		groups = append(groups, types.DuplicateGroup{
			HashAlgo: s.opts.HashAlgo,
			Hash:     h,
			Files:    files,
		})
	}
	// 依据哈希排序，稳定输出
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Hash == groups[j].Hash {
			return len(groups[i].Files) > len(groups[j].Files)
		}
		return groups[i].Hash < groups[j].Hash
	})
	// 保存缓存
	if s.opts.EnableHashCache {
		c.Save()
	}
	return groups, allFiles, nil
}

func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

func isSymlink(d fs.DirEntry) bool {
	if d.Type()&fs.ModeSymlink != 0 {
		return true
	}
	return false
}

func isWritable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func isSystemDir(p string) bool {
	p = filepath.Clean(p)
	switch runtime.GOOS {
	case "darwin":
		return p == "/System" || strings.HasPrefix(p, "/System/")
	case "linux":
		return p == "/usr" || strings.HasPrefix(p, "/usr/")
	case "windows":
		pp := strings.ToLower(p)
		return pp == `c:\windows` || strings.HasPrefix(pp, `c:\windows\`)
	default:
		return false
	}
}

// isSystemFile 判断常见系统/桌面元数据文件
func isSystemFile(base string) bool {
	b := strings.ToLower(base)
	switch runtime.GOOS {
	case "darwin":
		if b == ".ds_store" {
			return true
		}
		if strings.HasPrefix(base, "._") {
			return true
		}
		return b == ".localized"
	case "windows":
		return b == "thumbs.db" || b == "desktop.ini"
	default:
		return b == ".directory"
	}
}
