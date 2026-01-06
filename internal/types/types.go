package types

import "time"

// FileMeta 描述一个文件的元数据
type FileMeta struct {
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"modTime"`
	IsSymlink   bool      `json:"isSymlink"`
	HashAlgo    string    `json:"hashAlgo,omitempty"`
	HashValue   string    `json:"hashValue,omitempty"`
	AccessError string    `json:"accessError,omitempty"`
}

// DuplicateGroup 表示一组内容重复的文件（相同哈希）
type DuplicateGroup struct {
	HashAlgo string     `json:"hashAlgo"`
	Hash     string     `json:"hash"`
	Files    []FileMeta `json:"files"`
}

// ScanOptions 扫描配置
type ScanOptions struct {
	Dirs             []string `json:"dirs"`
	ExcludeDirs      []string `json:"excludeDirs"`
	SkipHidden       bool     `json:"skipHidden"`
	SkipSystemFiles  bool     `json:"skipSystemFiles"`
	FollowSymlinks   bool     `json:"followSymlinks"`
	HashAlgo         string   `json:"hashAlgo"`
	ChunkSizeBytes   int      `json:"chunkSizeBytes"`
	MaxWorkers       int      `json:"maxWorkers"`
	EnableHashCache  bool     `json:"enableHashCache"`
	HashCachePath    string   `json:"hashCachePath"`
	SkipReadOnly     bool     `json:"skipReadOnly"`
	SkipSystemDirs   bool     `json:"skipSystemDirs"`
	SkipSymlinkLoops bool     `json:"skipSymlinkLoops"`
}
