package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func EnsureSQLitePath(path string) error {
	if path == "" {
		return fmt.Errorf("sqlite 路径为空")
	}
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建 sqlite 目录失败 (%s): %w", dir, err)
	}

	file, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("创建 sqlite 文件失败 (%s): %w", path, err)
	}
	_ = file.Close()

	return nil
}

func EnsureDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("目录路径为空")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("创建目录失败 (%s): %w", path, err)
	}
	return nil
}
