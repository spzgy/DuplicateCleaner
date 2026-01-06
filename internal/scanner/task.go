package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"duplicatecleaner/internal/cache"
	"duplicatecleaner/internal/hash"
	"duplicatecleaner/internal/types"
)

type Task struct {
	opts           types.ScanOptions
	startTime      time.Time
	running        int32
	paused         int32
	stopped        int32
	mu             sync.Mutex
	progress       int
	allFiles       []types.FileMeta
	sizeBuckets    map[int64][]types.FileMeta
	currentBucket  int
	totalBuckets   int
	hashGroups     map[string][]types.FileMeta
	lastError      string
	enableHashCache bool
	cacheStore     cache.Store
}

// NewTask 创建新的扫描任务
func NewTask(opts types.ScanOptions) *Task {
	if opts.HashAlgo == "" {
		opts.HashAlgo = "sha256"
	}
	if opts.ChunkSizeBytes == 0 {
		opts.ChunkSizeBytes = 4 * 1024 * 1024
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = runtime.NumCPU()
	}
	t := &Task{
		opts:        opts,
		startTime:   time.Now(),
		sizeBuckets: map[int64][]types.FileMeta{},
		hashGroups:  map[string][]types.FileMeta{},
	}
	if opts.EnableHashCache {
		t.enableHashCache = true
		t.cacheStore.Load(opts.HashCachePath)
	}
	return t
}

// StartAsync 异步启动扫描任务
func (t *Task) StartAsync() {
	atomic.StoreInt32(&t.running, 1)
	go func() {
		t.buildBuckets()
		t.processBuckets()
		atomic.StoreInt32(&t.running, 0)
	}()
}

// Pause 暂停任务
func (t *Task) Pause() {
	atomic.StoreInt32(&t.paused, 1)
}

// Resume 恢复任务
func (t *Task) Resume() {
	atomic.StoreInt32(&t.paused, 0)
}

// Stop 结束任务（保留当前结果）
func (t *Task) Stop() {
	atomic.StoreInt32(&t.stopped, 1)
	atomic.StoreInt32(&t.running, 0)
	// 保存缓存（已有部分）
	if t.enableHashCache {
		t.cacheStore.Save()
	}
}

// Status 返回当前状态
func (t *Task) Status() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return map[string]any{
		"running":      atomic.LoadInt32(&t.running) == 1,
		"paused":       atomic.LoadInt32(&t.paused) == 1,
		"progress":     t.progress,
		"error":        t.lastError,
		"groupCount":   len(t.buildGroupsSnapshot()),
		"fileCount":    len(t.allFiles),
		"startTimeSec": int(time.Since(t.startTime).Seconds()),
	}
}

// Results 返回重复文件分组
func (t *Task) Results() []types.DuplicateGroup {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buildGroupsSnapshot()
}

func (t *Task) buildBuckets() {
	var allFiles []types.FileMeta
	visited := map[string]struct{}{}
	for _, root := range t.opts.Dirs {
		root = filepath.Clean(root)
		if t.opts.SkipSystemDirs && isSystemDir(root) {
			continue
		}
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				allFiles = append(allFiles, types.FileMeta{Path: p, AccessError: err.Error()})
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if t.opts.SkipHidden && isHidden(name) {
					return filepath.SkipDir
				}
				if t.opts.SkipSystemDirs && isSystemDir(p) {
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
			if t.opts.SkipHidden && isHidden(base) {
				return nil
			}
			if t.opts.SkipSystemFiles && isSystemFile(base) {
				return nil
			}
			if t.opts.SkipReadOnly {
				if !isWritable(p) {
					return nil
				}
			}
			isSym := false
			if d.Type()&os.ModeSymlink != 0 {
				isSym = true
			}
			if isSym && !t.opts.FollowSymlinks {
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
			if info.Size() > 0 {
				t.sizeBuckets[info.Size()] = append(t.sizeBuckets[info.Size()], meta)
			}
			return nil
		})
	}
	t.mu.Lock()
	t.allFiles = allFiles
	// 统计桶数
	for _, bucket := range t.sizeBuckets {
		if len(bucket) >= 2 {
			t.totalBuckets++
		}
	}
	t.mu.Unlock()
}

func (t *Task) processBuckets() {
	var wg sync.WaitGroup
	sem := make(chan struct{}, t.opts.MaxWorkers)
	// 为了可暂停/停止，按桶顺序逐个提交
	for _, bucket := range t.sizeBuckets {
		if len(bucket) < 2 {
			t.updateProgressStep()
			continue
		}
		if atomic.LoadInt32(&t.stopped) == 1 {
			break
		}
		// 等待暂停恢复
		for atomic.LoadInt32(&t.paused) == 1 {
			time.Sleep(200 * time.Millisecond)
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(files []types.FileMeta) {
			defer func() {
				<-sem
				wg.Done()
			}()
			for _, fm := range files {
				// 检查停止/暂停
				if atomic.LoadInt32(&t.stopped) == 1 {
					return
				}
				for atomic.LoadInt32(&t.paused) == 1 {
					time.Sleep(200 * time.Millisecond)
				}
				beforeInfo, _ := os.Stat(fm.Path)
				var hv string
				var err error
				if t.enableHashCache && beforeInfo != nil {
					if cached, ok := t.cacheStore.Get(fm.Path, beforeInfo.Size(), beforeInfo.ModTime(), t.opts.HashAlgo); ok {
						hv = cached
					}
				}
				if hv == "" {
					hv, err = hash.ComputeHash(fm.Path, t.opts.HashAlgo, t.opts.ChunkSizeBytes)
				}
				afterInfo, _ := os.Stat(fm.Path)
				if err != nil {
					fm.AccessError = err.Error()
				} else {
					if beforeInfo != nil && afterInfo != nil {
						if beforeInfo.ModTime() != afterInfo.ModTime() || beforeInfo.Size() != afterInfo.Size() {
							fm.AccessError = "文件在哈希计算过程中发生变化"
						}
					}
					fm.HashAlgo = t.opts.HashAlgo
					fm.HashValue = hv
					if t.enableHashCache && afterInfo != nil {
						t.cacheStore.Put(fm.Path, afterInfo.Size(), afterInfo.ModTime(), t.opts.HashAlgo, hv)
					}
				}
				t.mu.Lock()
				t.hashGroups[hv] = append(t.hashGroups[hv], fm)
				t.mu.Unlock()
			}
		}(bucket)
		wg.Wait()
		t.updateProgressStep()
	}
	// 完成，保存缓存
	if t.enableHashCache {
		t.cacheStore.Save()
	}
}

func (t *Task) updateProgressStep() {
	t.mu.Lock()
	t.currentBucket++
	if t.totalBuckets == 0 {
		t.progress = 100
	} else {
		p := int(float64(t.currentBucket) / float64(t.totalBuckets) * 100.0)
		if p > 100 {
			p = 100
		}
		if p < t.progress {
			p = t.progress
		}
		t.progress = p
	}
	t.mu.Unlock()
}

func (t *Task) buildGroupsSnapshot() []types.DuplicateGroup {
	var groups []types.DuplicateGroup
	for h, files := range t.hashGroups {
		if h == "" {
			continue
		}
		if len(files) < 2 {
			continue
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		groups = append(groups, types.DuplicateGroup{
			HashAlgo: t.opts.HashAlgo,
			Hash:     h,
			Files:    files,
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Hash == groups[j].Hash {
			return len(groups[i].Files) > len(groups[j].Files)
		}
		return groups[i].Hash < groups[j].Hash
	})
	return groups
}
