package rules

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"duplicatecleaner/internal/types"
)

// RulePriority 定义规则优先级
// 越靠前优先级越高
type RulePriority []string

// RuleSet 规则集合
type RuleSet struct {
	Priority           RulePriority `json:"priority"`
	KeepDirs           []string     `json:"keepDirs"`
	KeepLatestModify   bool         `json:"keepLatestModify"`
	KeepEarliestCreate bool         `json:"keepEarliestCreate"`
	KeepHighestVersion bool         `json:"keepHighestVersion"`
}

// Apply 对重复组应用规则，返回保留文件与待删除文件
func Apply(group types.DuplicateGroup, rs RuleSet) (keep types.FileMeta, deletes []types.FileMeta) {
	candidates := append([]types.FileMeta(nil), group.Files...)

	// 归一化 keepDirs
	var normDirs []string
	for _, d := range rs.KeepDirs {
		normDirs = append(normDirs, filepath.Clean(d))
	}

	// 按优先级逐条应用
	for _, key := range rs.Priority {
		switch key {
		case "指定文件夹":
			if len(normDirs) > 0 {
				if k, ok := preferKeepDirs(candidates, normDirs); ok {
					candidates = []types.FileMeta{k}
				}
			}
		case "最新修改":
			if rs.KeepLatestModify {
				candidates = []types.FileMeta{keepByTime(candidates, true)}
			}
		case "最早创建":
			if rs.KeepEarliestCreate {
				candidates = []types.FileMeta{keepByTime(candidates, false)}
			}
		case "最大版本号":
			if rs.KeepHighestVersion {
				candidates = []types.FileMeta{keepByVersion(candidates)}
			}
		}
	}

	// 兜底：路径字母序靠前者
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	keep = candidates[0]

	// 删除清单
	for _, f := range group.Files {
		if f.Path != keep.Path {
			deletes = append(deletes, f)
		}
	}
	return
}

func preferKeepDirs(files []types.FileMeta, dirs []string) (types.FileMeta, bool) {
	// 若多个命中，取路径字母序靠前的
	var hits []types.FileMeta
	for _, f := range files {
		for _, d := range dirs {
			if strings.HasPrefix(filepath.Clean(f.Path), d) {
				hits = append(hits, f)
				break
			}
		}
	}
	if len(hits) == 0 {
		return types.FileMeta{}, false
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Path < hits[j].Path })
	return hits[0], true
}

func keepByTime(files []types.FileMeta, latest bool) types.FileMeta {
	var best types.FileMeta
	var bestTime time.Time
	for i, f := range files {
		t := f.ModTime
		if i == 0 {
			best, bestTime = f, t
			continue
		}
		if latest {
			if t.After(bestTime) {
				best, bestTime = f, t
			}
		} else {
			if t.Before(bestTime) {
				best, bestTime = f, t
			}
		}
	}
	return best
}

var verRe = regexp.MustCompile(`v?(\d+)(\.\d+)*`)

func versionScore(name string) []int {
	name = strings.ToLower(name)
	m := verRe.FindString(name)
	if m == "" {
		return []int{0}
	}
	parts := strings.Split(strings.TrimPrefix(m, "v"), ".")
	var nums []int
	for _, p := range parts {
		// 忽略转换错误视为0
		var n int
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				continue
			}
			n = n*10 + int(p[i]-'0')
		}
		nums = append(nums, n)
	}
	return nums
}

func keepByVersion(files []types.FileMeta) types.FileMeta {
	type rec struct {
		f     types.FileMeta
		score []int
	}
	var rs []rec
	for _, f := range files {
		rs = append(rs, rec{f: f, score: versionScore(filepath.Base(f.Path))})
	}
	sort.Slice(rs, func(i, j int) bool {
		a, b := rs[i].score, rs[j].score
		// 逐段比较
		for k := 0; k < len(a) || k < len(b); k++ {
			ai, bi := 0, 0
			if k < len(a) {
				ai = a[k]
			}
			if k < len(b) {
				bi = b[k]
			}
			if ai != bi {
				return ai > bi
			}
		}
		// 版本号相同则路径字母序
		return rs[i].f.Path < rs[j].f.Path
	})
	return rs[0].f
}
