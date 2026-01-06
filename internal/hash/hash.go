package hash

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
)

// ComputeHash 计算文件哈希，支持 MD5 与 SHA-256，按块读取以兼顾大文件性能
func ComputeHash(path string, algo string, chunkSize int) (string, error) {
	var h hash.Hash
	switch algo {
	case "md5", "MD5":
		h = md5.New()
	case "sha256", "SHA-256", "SHA256":
		h = sha256.New()
	default:
		return "", errors.New("不支持的哈希算法")
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if chunkSize <= 0 {
		chunkSize = 4 * 1024 * 1024
	}
	buf := make([]byte, chunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, werr := h.Write(buf[:n]); werr != nil {
				return "", werr
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
