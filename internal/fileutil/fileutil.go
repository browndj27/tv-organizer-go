package fileutil

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tvorganizer/internal/config"
)

var copyBufPool = sync.Pool{
	New: func() any { return make([]byte, config.FileBufferSize) },
}

// CopyWithHash copies src to dst and returns the MD5 hex digest of the source
// data, computed during the single read pass — no second read needed.
func CopyWithHash(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()

	h := md5.New()
	buf := copyBufPool.Get().([]byte)
	defer copyBufPool.Put(buf)
	if _, err = io.CopyBuffer(io.MultiWriter(out, h), in, buf); err != nil {
		return "", err
	}
	if err = out.Sync(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%X", h.Sum(nil)), nil
}

// WalkVideoFiles returns every file under root whose extension is in formats.
func WalkVideoFiles(root string, formats map[string]bool) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if formats[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
		return nil
	})
	return files
}
