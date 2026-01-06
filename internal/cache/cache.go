package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Entry 描述缓存条目
type Entry struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Algo    string    `json:"algo"`
	Hash    string    `json:"hash"`
}

// Store 管理哈希缓存
type Store struct {
	Path string
	m    map[string]Entry
}

// Load 加载缓存文件
func (s *Store) Load(path string) {
	if path == "" {
		path = filepath.Join(".cache", "hashes.json")
	}
	s.Path = path
	s.m = map[string]Entry{}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.m)
}

// Save 写回缓存文件
func (s *Store) Save() {
	if s.Path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.Path), 0o755)
	_ = os.WriteFile(s.Path, mustJSON(s.m), 0o644)
}

// Get 查找缓存
func (s *Store) Get(path string, size int64, mt time.Time, algo string) (string, bool) {
	if s.m == nil {
		return "", false
	}
	key := path
	e, ok := s.m[key]
	if !ok {
		return "", false
	}
	if e.Size == size && e.ModTime.Equal(mt) && e.Algo == algo {
		return e.Hash, true
	}
	return "", false
}

// Put 写入缓存
func (s *Store) Put(path string, size int64, mt time.Time, algo, hash string) {
	if s.m == nil {
		s.m = map[string]Entry{}
	}
	s.m[path] = Entry{Path: path, Size: size, ModTime: mt, Algo: algo, Hash: hash}
}

func mustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}
