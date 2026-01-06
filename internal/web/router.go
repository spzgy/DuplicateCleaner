package web

import (
	"duplicatecleaner/internal/backup"
	"duplicatecleaner/internal/logging"
	"duplicatecleaner/internal/rules"
	"duplicatecleaner/internal/scanner"
	"duplicatecleaner/internal/types"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// RouterState 保存路由层的全局状态
type RouterState struct {
	startTime   time.Time
	mu          sync.RWMutex
	scanRunning bool
	progress    int
	groups      []types.DuplicateGroup
	allFiles    []types.FileMeta
	opts        types.ScanOptions
	ruleSet     rules.RuleSet
	lastError   string
	backupDir   string
	task        *scanner.Task
}

// NewRouter 创建并返回 HTTP 路由
// 提供静态页面与基础 API 占位符
func NewRouter() http.Handler {
	state := &RouterState{startTime: time.Now()}
	state.backupDir = filepath.Join(".", "Duplicate_Backup")
	mux := http.NewServeMux()

	// 页面
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tpl := template.Must(template.New("index").Parse(indexHTML))
		_ = tpl.Execute(w, nil)
	})

	// API: 健康检查
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"ok":        true,
			"uptimeSec": int(time.Since(state.startTime).Seconds()),
			"goos":      runtime.GOOS,
			"goarch":    runtime.GOARCH,
		}
		writeJSON(w, resp, http.StatusOK)
	})

	// API: 启动扫描
	mux.HandleFunc("/api/scan/start", func(w http.ResponseWriter, r *http.Request) {
		var opts types.ScanOptions
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			writeJSON(w, map[string]string{"error": "无效参数"}, http.StatusBadRequest)
			return
		}
		if len(opts.Dirs) == 0 {
			writeJSON(w, map[string]string{"error": "请提供至少一个扫描目录"}, http.StatusBadRequest)
			return
		}
		state.mu.Lock()
		if state.task != nil {
			st := state.task.Status()
			if st["running"].(bool) {
				state.mu.Unlock()
				writeJSON(w, map[string]string{"message": "扫描已在进行中"}, http.StatusConflict)
				return
			}
		}
		state.opts = opts
		state.task = scanner.NewTask(opts)
		state.task.StartAsync()
		state.scanRunning = true
		state.progress = 0
		state.groups = nil
		state.allFiles = nil
		state.lastError = ""
		state.mu.Unlock()

		logging.LogEvent("logs", logging.Event{Type: "scan_start", Data: opts})
		writeJSON(w, map[string]string{"message": "扫描任务已提交"}, http.StatusAccepted)
	})

	// API: 扫描状态
	mux.HandleFunc("/api/scan/status", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		var resp map[string]any
		if state.task != nil {
			resp = state.task.Status()
		} else {
			resp = map[string]any{
				"running":  false,
				"paused":   false,
				"progress": 0,
				"error":    "",
				"groups":   0,
			}
		}
		state.mu.RUnlock()
		writeJSON(w, resp, http.StatusOK)
	})

	// API: 扫描结果
	mux.HandleFunc("/api/scan/results", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		var groups []types.DuplicateGroup
		if state.task != nil {
			groups = state.task.Results()
		}
		state.mu.RUnlock()
		writeJSON(w, map[string]any{"groups": groups}, http.StatusOK)
	})

	// API: 暂停扫描
	mux.HandleFunc("/api/scan/pause", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.task == nil {
			writeJSON(w, map[string]string{"error": "无进行中的任务"}, http.StatusBadRequest)
			return
		}
		state.task.Pause()
		logging.LogEvent("logs", logging.Event{Type: "scan_pause"})
		writeJSON(w, map[string]string{"message": "任务已暂停"}, http.StatusOK)
	})

	// API: 恢复扫描
	mux.HandleFunc("/api/scan/resume", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.task == nil {
			writeJSON(w, map[string]string{"error": "无进行中的任务"}, http.StatusBadRequest)
			return
		}
		state.task.Resume()
		logging.LogEvent("logs", logging.Event{Type: "scan_resume"})
		writeJSON(w, map[string]string{"message": "任务已恢复"}, http.StatusOK)
	})

	// API: 结束扫描（保留当前结果）
	mux.HandleFunc("/api/scan/stop", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.task == nil {
			writeJSON(w, map[string]string{"error": "无进行中的任务"}, http.StatusBadRequest)
			return
		}
		state.task.Stop()
		logging.LogEvent("logs", logging.Event{Type: "scan_stop"})
		writeJSON(w, map[string]string{"message": "任务已结束，结果已保留"}, http.StatusOK)
	})

	// API: 设置规则
	mux.HandleFunc("/api/rules", func(w http.ResponseWriter, r *http.Request) {
		var rs rules.RuleSet
		if err := json.NewDecoder(r.Body).Decode(&rs); err != nil {
			writeJSON(w, map[string]string{"error": "无效规则参数"}, http.StatusBadRequest)
			return
		}
		state.mu.Lock()
		state.ruleSet = rs
		state.mu.Unlock()
		logging.LogEvent("logs", logging.Event{Type: "rules_update", Data: rs})
		writeJSON(w, map[string]string{"message": "规则已更新"}, http.StatusOK)
	})

	// API: 删除预览
	mux.HandleFunc("/api/delete/preview", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		rs := state.ruleSet
		var groups []types.DuplicateGroup
		if state.task != nil {
			groups = state.task.Results()
		}
		state.mu.RUnlock()
		plan := make([]map[string]any, 0)
		for _, g := range groups {
			keep, dels := rules.Apply(g, rs)
			delPaths := make([]string, 0, len(dels))
			for _, d := range dels {
				delPaths = append(delPaths, d.Path)
			}
			plan = append(plan, map[string]any{
				"hash":   g.Hash,
				"keep":   keep.Path,
				"delete": delPaths,
			})
		}
		logging.LogEvent("logs", logging.Event{Type: "delete_preview", Data: plan})
		writeJSON(w, map[string]any{"plan": plan}, http.StatusOK)
	})

	// API: 删除确认（移动到备份目录）
	mux.HandleFunc("/api/delete/confirm", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CleanEmptyDirs bool   `json:"cleanEmptyDirs"`
			DeleteMode     string `json:"deleteMode"`   // "backup" | "trash" | "permanent"
			DirectToTrash  bool   `json:"directToTrash"` // 兼容旧参数：true 等同于 "trash"
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		state.mu.RLock()
		rs := state.ruleSet
		var groups []types.DuplicateGroup
		if state.task != nil {
			groups = state.task.Results()
		}
		backupDir := state.backupDir
		state.mu.RUnlock()
		var toDelete []string
		for _, g := range groups {
			_, dels := rules.Apply(g, rs)
			for _, d := range dels {
				toDelete = append(toDelete, d.Path)
			}
		}
		var moved []string
		var failed map[string]string
		var manifest string
		mode := "backup"
		if req.DeleteMode == "" && req.DirectToTrash {
			req.DeleteMode = "trash"
		}
		switch req.DeleteMode {
		case "trash":
			moved, failed = backup.MoveToTrash(toDelete)
			manifest = ""
			mode = "trash"
		case "permanent":
			moved, failed = backup.DeletePermanently(toDelete)
			manifest = ""
			mode = "permanent"
		default:
			moved, failed, manifest = backup.MoveToBackupWithManifest(toDelete, backupDir)
			mode = "backup"
		}
		var cleaned []string
		var cleanFailed map[string]string
		if req.CleanEmptyDirs {
			cleaned, cleanFailed = backup.CleanEmptyDirs(toDelete, state.opts.Dirs)
		}
		logging.LogEvent("logs", logging.Event{
			Type: "delete_confirm",
			Data: map[string]any{
				"mode":      mode,
				"backupDir": backupDir,
				"moved":     moved,
				"failed":    failed,
				"cleaned":   cleaned,
				"cleanFail": cleanFailed,
				"manifest":  manifest,
			},
		})
		writeJSON(w, map[string]any{
			"mode":      mode,
			"backupDir": backupDir,
			"moved":     moved,
			"failed":    failed,
			"cleaned":   cleaned,
			"cleanFail": cleanFailed,
			"manifest":  manifest,
		}, http.StatusOK)
	})

	// API: 恢复上次删除
	mux.HandleFunc("/api/delete/restore", func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		backupDir := state.backupDir
		state.mu.RUnlock()
		restored, failed := backup.RestoreLast(backupDir)
		logging.LogEvent("logs", logging.Event{
			Type: "delete_restore",
			Data: map[string]any{
				"restored": restored,
				"failed":   failed,
			},
		})
		writeJSON(w, map[string]any{
			"restored": restored,
			"failed":   failed,
		}, http.StatusOK)
	})

	// 静态资源占位（若未来需要）
	assetsDir := filepath.Join("web", "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(assetsDir))))
	_ = os.MkdirAll(assetsDir, 0o755) // 确保目录存在

	return mux
}

// writeJSON 输出 JSON 响应
func writeJSON(w http.ResponseWriter, v any, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>DuplicateCleaner</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Arial, "Noto Sans", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif; margin: 2rem; }
    .bar { display: flex; gap: 1rem; align-items: center; margin-bottom: 1rem; flex-wrap: wrap; }
    button { padding: .5rem 1rem; }
    pre { background: #f7f7f7; padding: 1rem; border-radius: 6px; overflow: auto; }
    input, select { padding: .4rem .6rem; }
    .col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
    .card { border: 1px solid #ddd; border-radius: 8px; padding: 1rem; }
    .bar .progressBtn, .bar .resultBtn { display: none; }
    .progress { width: 100%; height: 10px; background: #eee; border-radius: 6px; overflow: hidden; }
    .progress > div { height: 100%; background: #4caf50; width: 0%; transition: width .3s; }
    .opt { display: inline-flex; align-items: center; gap: .25rem; margin-right: 2rem; }
  </style>
  <script>
    const I18N = {
      zh: {
        app_title: "DuplicateCleaner",
        lang_label: "语言",
        lang_zh: "中文",
        lang_en: "English",
        scan_config: "扫描配置",
        input_dirs_label: "扫描目录（逗号分隔）",
        input_dirs_placeholder: "/Users/xxx/Download,/Users/xxx/Documents",
        algorithm: "算法",
        concurrency: "并发工作数",
        chunk_size: "分块大小(MB)",
        skip_hidden: "跳过隐藏",
        skip_system_files: "跳过系统文件",
        follow_symlinks: "跟随软链接",
        skip_system_dirs: "跳过系统目录",
        start_scan: "启动扫描",
        pause_scan: "暂停扫描",
        resume_scan: "恢复扫描",
        stop_scan: "结束扫描",
        health_check: "健康检查",
        scan_status: "扫描状态",
        scan_results: "扫描结果",
        delete_rules: "删除规则",
        priority_label: "优先级",
        priority_keep_dirs: "指定文件夹",
        priority_latest_modify: "最新修改",
        priority_earliest_create: "最早创建",
        priority_highest_version: "最大版本号",
        keep_dirs_label: "保留目录（逗号分隔）",
        keep_dirs_placeholder: "/Users/xxx/Keep,/Users/xxx/Archive",
        keep_latest_modify: "保留最新修改",
        keep_earliest_create: "保留最早创建",
        keep_highest_version: "保留最大版本号",
        delete_preview: "删除预览",
        clean_empty_dirs: "清空空文件夹",
        direct_trash: "放入回收站",
        confirm_delete: "确认删除",
        restore_last: "恢复上次删除",
        rules_status: "规则状态",
        preview_result: "删除预览",
        delete_result: "删除结果",
        restore_result: "恢复结果",
        confirm_delete_prompt: "确认执行删除（移动到备份目录）吗？",
        confirm_delete_trash_prompt: "确认删除并放入系统回收站吗？（可在系统回收站恢复）",
        confirm_delete_perm_prompt: "确认直接永久删除吗？（不可恢复，谨慎操作）",
        confirm_restore_prompt: "确认恢复上次删除的文件吗？（若目标已存在将跳过）"
      },
      en: {
        app_title: "DuplicateCleaner",
        lang_label: "Language",
        lang_zh: "中文",
        lang_en: "English",
        scan_config: "Scan Settings",
        input_dirs_label: "Directories (comma-separated)",
        input_dirs_placeholder: "/Users/xxx/Download,/Users/xxx/Documents",
        algorithm: "Algorithm",
        concurrency: "Workers",
        chunk_size: "Chunk Size (MB)",
        skip_hidden: "Skip Hidden",
        skip_system_files: "Skip System Files",
        follow_symlinks: "Follow Symlinks",
        skip_system_dirs: "Skip System Dirs",
        start_scan: "Start Scan",
        pause_scan: "Pause",
        resume_scan: "Resume",
        stop_scan: "Stop",
        health_check: "Health",
        scan_status: "Status",
        scan_results: "Results",
        delete_rules: "Delete Rules",
        priority_label: "Priority",
        priority_keep_dirs: "Prefer Keep Dirs",
        priority_latest_modify: "Latest Modified",
        priority_earliest_create: "Earliest Created",
        priority_highest_version: "Highest Version",
        keep_dirs_label: "Keep Dirs (comma-separated)",
        keep_dirs_placeholder: "/Users/xxx/Keep,/Users/xxx/Archive",
        keep_latest_modify: "Keep Latest Modified",
        keep_earliest_create: "Keep Earliest Created",
        keep_highest_version: "Keep Highest Version",
        delete_preview: "Delete Preview",
        clean_empty_dirs: "Clean Empty Folders",
        direct_trash: "Direct Delete (Trash)",
        confirm_delete: "Confirm Delete",
        restore_last: "Restore Last",
        rules_status: "Rules Status",
        preview_result: "Delete Preview",
        delete_result: "Delete Result",
        restore_result: "Restore Result",
        confirm_delete_prompt: "Confirm delete (move to backup)?",
        confirm_delete_trash_prompt: "Confirm delete to OS Trash? (restore via system Trash)",
        confirm_delete_perm_prompt: "Confirm permanent delete? (cannot be recovered)",
        confirm_restore_prompt: "Restore last deleted files? (skip if exists)"
      }
    };
    function getDefaultLang() {
      const l = (navigator.language || "en").toLowerCase();
      return l.startsWith("zh") ? "zh" : "en";
    }
    function getLang() {
      return localStorage.getItem("dc_lang") || getDefaultLang();
    }
    function setLang(l) {
      localStorage.setItem("dc_lang", l);
      applyI18n();
    }
    function t(key) {
      const lang = getLang();
      const dict = I18N[lang] || I18N.zh;
      return dict[key] || key;
    }
    function applyI18n() {
      document.title = t("app_title");
      document.documentElement.lang = getLang() === "zh" ? "zh-CN" : "en";
      const mapping = [
        ["title-app","app_title"],
        ["scan-config","scan_config"],
        ["label-input-dirs","input_dirs_label"],
        ["label-algo","algorithm"],
        ["label-workers","concurrency"],
        ["label-chunk","chunk_size"],
        ["label-skip-hidden","skip_hidden"],
        ["label-skip-system-files","skip_system_files"],
        ["label-follow-symlinks","follow_symlinks"],
        ["label-skip-system-dirs","skip_system_dirs"],
        ["btn-start","start_scan"],
        ["btn-pause","pause_scan"],
        ["btn-resume","resume_scan"],
        ["btn-stop","stop_scan"],
        ["health-title","health_check"],
        ["status-title","scan_status"],
        ["results-title","scan_results"],
        ["delete-rules","delete_rules"],
        ["priority-label","priority_label"],
        ["keep-dirs-label","keep_dirs_label"],
        ["label-keep-latest","keep_latest_modify"],
        ["label-keep-earliest","keep_earliest_create"],
        ["label-keep-version","keep_highest_version"],
        ["btn-preview","delete_preview"],
        ["label-clean-empty","clean_empty_dirs"],
        ["label-delete-mode","delete_mode"],
        ["btn-confirm","confirm_delete"],
        ["btn-restore","restore_last"],
        ["rules-status-title","rules_status"],
        ["preview-title","preview_result"],
        ["confirm-title","delete_result"],
        ["restore-title","restore_result"]
      ];
      for (const [id,key] of mapping) {
        const el = document.getElementById(id);
        if (el) el.textContent = t(key);
      }
      const dirs = document.getElementById("dirs");
      if (dirs) dirs.placeholder = t("input_dirs_placeholder");
      const keepDirs = document.getElementById("keepDirs");
      if (keepDirs) keepDirs.placeholder = t("keep_dirs_placeholder");
      const p1o = document.querySelectorAll("#p1 option");
      const p2o = document.querySelectorAll("#p2 option");
      const p3o = document.querySelectorAll("#p3 option");
      const labelKeys = ["priority_keep_dirs","priority_latest_modify","priority_earliest_create","priority_highest_version"];
      function applyOptions(opts) {
        for (let i=0;i<opts.length;i++) {
          const opt = opts[i];
          const val = opt.getAttribute("value");
          if (val === "指定文件夹") opt.textContent = t("priority_keep_dirs");
          else if (val === "最新修改") opt.textContent = t("priority_latest_modify");
          else if (val === "最早创建") opt.textContent = t("priority_earliest_create");
          else if (val === "最大版本号") opt.textContent = t("priority_highest_version");
        }
      }
      applyOptions(p1o); applyOptions(p2o); applyOptions(p3o);
      const langSel = document.getElementById("lang");
      if (langSel) langSel.value = getLang();
      const modeSel = document.getElementById("deleteMode");
      if (modeSel) {
        const opts = modeSel.querySelectorAll("option");
        opts.forEach(opt => {
          const v = opt.getAttribute("value");
          if (v === "backup") opt.textContent = getLang()==="zh" ? "删除进入备份" : "Delete to Backup";
          else if (v === "trash") opt.textContent = getLang()==="zh" ? "删除进入回收站" : "Delete to Trash";
          else if (v === "permanent") opt.textContent = getLang()==="zh" ? "直接删除(谨慎操作)" : "Permanent Delete (Danger)";
        });
      }
    }
    async function ping() {
      const res = await fetch('/api/health');
      const data = await res.json();
      document.getElementById('health').textContent = JSON.stringify(data, null, 2);
    }
    async function startScan() {
      const dirs = document.getElementById('dirs').value.split(',').map(s=>s.trim()).filter(Boolean);
      const algo = document.getElementById('algo').value;
      const skipHidden = document.getElementById('skipHidden').checked;
      const skipSystemFiles = document.getElementById('skipSystemFiles').checked;
      const followSymlinks = document.getElementById('followSymlinks').checked;
      const skipSystemDirs = document.getElementById('skipSystemDirs').checked;
      const maxWorkers = parseInt(document.getElementById('maxWorkers').value || '0', 10);
      const chunkSizeBytes = parseInt(document.getElementById('chunkSize').value || '0', 10) * 1024 * 1024;
      const res = await fetch('/api/scan/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dirs, hashAlgo: algo, skipHidden, skipSystemFiles, followSymlinks, skipSystemDirs, maxWorkers, chunkSizeBytes })
      });
      const data = await res.json();
      document.getElementById('status').textContent = JSON.stringify(data, null, 2);
    }
    async function status() {
      const res = await fetch('/api/scan/status');
      const data = await res.json();
      document.getElementById('status').textContent = JSON.stringify(data, null, 2);
    }
    async function loadResults() {
      const res = await fetch('/api/scan/results');
      const data = await res.json();
      document.getElementById('results').textContent = JSON.stringify(data, null, 2);
    }
    async function updateRules() {
      const keepDirs = document.getElementById('keepDirs').value.split(',').map(s=>s.trim()).filter(Boolean);
      const selects = [document.getElementById('p1').value, document.getElementById('p2').value, document.getElementById('p3').value];
      const seen = new Set();
      const priority = [];
      for (const k of selects) { if (!seen.has(k)) { seen.add(k); priority.push(k); } }
      const keepLatestModify = document.getElementById('keepLatestModify').checked;
      const keepEarliestCreate = document.getElementById('keepEarliestCreate').checked;
      const keepHighestVersion = document.getElementById('keepHighestVersion').checked;
      const res = await fetch('/api/rules', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keepDirs, priority, keepLatestModify, keepEarliestCreate, keepHighestVersion })
      });
      const data = await res.json();
      document.getElementById('rulesStatus').textContent = JSON.stringify(data, null, 2);
    }
    async function previewDelete() {
      const res = await fetch('/api/delete/preview', { method: 'POST' });
      const data = await res.json();
      document.getElementById('preview').textContent = JSON.stringify(data, null, 2);
    }
    async function confirmDelete() {
      const mode = document.getElementById('deleteMode').value || 'backup';
      const promptKey = mode === 'trash' ? 'confirm_delete_trash_prompt' : (mode === 'permanent' ? 'confirm_delete_perm_prompt' : 'confirm_delete_prompt');
      const ok = confirm(t(promptKey));
      if (!ok) return;
      const cleanEmptyDirs = document.getElementById('cleanEmptyDirs').checked;
      const res = await fetch('/api/delete/confirm', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ cleanEmptyDirs, deleteMode: mode }) });
      const data = await res.json();
      document.getElementById('confirm').textContent = JSON.stringify(data, null, 2);
    }
    async function restoreLast() {
      const ok = confirm(t('confirm_restore_prompt'));
      if (!ok) return;
      const res = await fetch('/api/delete/restore', { method: 'POST' });
      const data = await res.json();
      document.getElementById('restore').textContent = JSON.stringify(data, null, 2);
    }
    function debounce(fn, wait) {
      let t = null;
      return function(...args) {
        if (t) clearTimeout(t);
        t = setTimeout(()=>fn.apply(this, args), wait);
      }
    }
    let pollId = null;
    function ensurePolling() {
      if (pollId) return;
      pollId = setInterval(refreshStatusAndResults, 1000);
    }
    async function refreshStatusAndResults() {
      try {
        const sres = await fetch('/api/scan/status');
        const sdata = await sres.json();
        document.getElementById('status').textContent = JSON.stringify(sdata, null, 2);
        const p = Math.max(0, Math.min(100, sdata.progress || 0));
        const pf = document.getElementById('pfill');
        if (pf) pf.style.width = p + '%';
        const rres = await fetch('/api/scan/results');
        const rdata = await rres.json();
        document.getElementById('results').textContent = JSON.stringify(rdata, null, 2);
      } catch (e) {}
    }
    function bindRuleAutoUpdate() {
      const ids = ['p1','p2','p3','keepLatestModify','keepEarliestCreate','keepHighestVersion'];
      ids.forEach(id => {
        const el = document.getElementById(id);
        if (el) el.addEventListener('change', updateRules);
      });
      const kd = document.getElementById('keepDirs');
      if (kd) kd.addEventListener('input', debounce(updateRules, 400));
    }
    function initLang() {
      const sel = document.getElementById('lang');
      if (sel) sel.addEventListener('change', (e)=>setLang(e.target.value));
      applyI18n();
    }
    window.onload = () => { initLang(); ping(); ensurePolling(); bindRuleAutoUpdate(); };
  </script>
</head>
<body>
  <div class="bar" style="justify-content: space-between;">
    <h1 id="title-app">DuplicateCleaner</h1>
    <div class="opt">
      <label id="lang-label">Language</label>
      <select id="lang">
        <option value="en">English</option>
        <option value="zh">中文</option>
      </select>
    </div>
  </div>
  <div class="col">
    <div class="card">
      <h3 id="scan-config">扫描配置</h3>
      <div class="bar">
        <label id="label-input-dirs">扫描目录（逗号分隔）</label>
        <input id="dirs" placeholder="/Users/xxx/Download,/Users/xxx/Documents" style="flex: 1">
      </div>
      <div class="bar">
        <label id="label-algo">算法</label>
        <select id="algo">
          <option value="sha256">SHA-256</option>
          <option value="md5">MD5</option>
        </select>
        <label id="label-workers">并发工作数</label><input id="maxWorkers" type="number" min="1" placeholder="CPU数" style="width: 6rem">
        <label id="label-chunk">分块大小(MB)</label><input id="chunkSize" type="number" min="1" placeholder="4" style="width: 6rem">
      </div>
      <div class="bar">
        <span class="opt"><label id="label-skip-hidden">跳过隐藏</label><input type="checkbox" id="skipHidden" checked></span>
        <span class="opt"><label id="label-skip-system-files">跳过系统文件</label><input type="checkbox" id="skipSystemFiles" checked></span>
        <span class="opt"><label id="label-follow-symlinks">跟随软链接</label><input type="checkbox" id="followSymlinks"></span>
        <span class="opt"><label id="label-skip-system-dirs">跳过系统目录</label><input type="checkbox" id="skipSystemDirs" checked></span>
      </div>
      <div class="bar">
        <button id="btn-start" onclick="startScan()">启动扫描</button>
        <button id="btn-pause" onclick="fetch('/api/scan/pause', {method:'POST'}).then(r=>r.json()).then(()=>{})">暂停扫描</button>
        <button id="btn-resume" onclick="fetch('/api/scan/resume', {method:'POST'}).then(r=>r.json()).then(()=>{})">恢复扫描</button>
        <button id="btn-stop" onclick="fetch('/api/scan/stop', {method:'POST'}).then(r=>r.json()).then(()=>{})">结束扫描</button>
      </div>
      <div class="progress"><div id="pfill"></div></div>
      <h4 id="health-title">健康检查</h4>
      <pre id="health"></pre>
      <h4 id="status-title">扫描状态</h4>
      <pre id="status"></pre>
      <h4 id="results-title">扫描结果</h4>
      <pre id="results"></pre>
    </div>
    <div class="card">
      <h3 id="delete-rules">删除规则</h3>
      <div class="bar">
        <label id="priority-label">优先级</label>
        <select id="p1">
          <option value="指定文件夹">指定文件夹</option>
          <option value="最新修改">最新修改</option>
          <option value="最早创建">最早创建</option>
          <option value="最大版本号">最大版本号</option>
        </select>
        <select id="p2">
          <option value="最新修改">最新修改</option>
          <option value="指定文件夹">指定文件夹</option>
          <option value="最早创建">最早创建</option>
          <option value="最大版本号">最大版本号</option>
        </select>
        <select id="p3">
          <option value="最大版本号">最大版本号</option>
          <option value="指定文件夹">指定文件夹</option>
          <option value="最新修改">最新修改</option>
          <option value="最早创建">最早创建</option>
        </select>
      </div>
      <div class="bar">
        <label id="keep-dirs-label">保留目录（逗号分隔）</label>
        <input id="keepDirs" placeholder="/Users/xxx/Keep,/Users/xxx/Archive" style="width: 100%">
      </div>
      <div class="bar">
        <span class="opt"><label id="label-keep-latest">保留最新修改</label><input type="checkbox" id="keepLatestModify" checked></span>
        <span class="opt"><label id="label-keep-earliest">保留最早创建</label><input type="checkbox" id="keepEarliestCreate"></span>
        <span class="opt"><label id="label-keep-version">保留最大版本号</label><input type="checkbox" id="keepHighestVersion"></span>
      </div>
      <div class="bar">
        <button id="btn-preview" onclick="previewDelete()">删除预览</button>
        <span class="opt"><label id="label-clean-empty">清空空文件夹</label><input type="checkbox" id="cleanEmptyDirs" checked></span>
        <span class="opt"><label id="label-delete-mode">删除模式</label>
          <select id="deleteMode">
            <option value="backup">删除进入备份</option>
            <option value="trash">删除进入回收站</option>
            <option value="permanent">直接删除(谨慎操作)</option>
          </select>
        </span>
        <button id="btn-confirm" onclick="confirmDelete()">确认删除</button>
        <button id="btn-restore" onclick="restoreLast()">恢复上次删除</button>
      </div>
      <h4 id="rules-status-title">规则状态</h4>
      <pre id="rulesStatus"></pre>
      <h4 id="preview-title">删除预览</h4>
      <pre id="preview"></pre>
      <h4 id="confirm-title">删除结果</h4>
      <pre id="confirm"></pre>
      <h4 id="restore-title">恢复结果</h4>
      <pre id="restore"></pre>
    </div>
  </div>
</body>
</html>`
