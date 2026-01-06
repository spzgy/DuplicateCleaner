package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Event 表示一次日志事件
type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message,omitempty"`
	Data    any       `json:"data,omitempty"`
}

// LogEvent 追加事件到日志文件（JSONL）
func LogEvent(logDir string, e Event) {
	if logDir == "" {
		logDir = "logs"
	}
	_ = os.MkdirAll(logDir, 0o755)
	path := filepath.Join(logDir, "duplicate_cleaner.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	enc := json.NewEncoder(f)
	_ = enc.Encode(e)
}
