package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxBytes = 32 * 1024

func Open(root string) (*os.Root, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("workspace root is not a directory")
	}
	return os.OpenRoot(root)
}

func Clean(path string) (string, error) {
	if path == "" || path == "." {
		return ".", nil
	}
	if filepath.IsAbs(path) || strings.Contains(path, "\\") || strings.Contains(path, ":") {
		return "", errors.New("path is not inside the workspace")
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("path is not inside the workspace")
	}
	return cleaned, nil
}

func List(root *os.Root, path string) ([]string, error) {
	cleaned, err := Clean(path)
	if err != nil {
		return nil, err
	}
	if err := rejectLink(root, cleaned); err != nil {
		return nil, err
	}
	directory, err := root.Open(cleaned)
	if err != nil {
		return nil, errors.New("path is not inside the workspace")
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, errors.New("path is not inside the workspace")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	return names, nil
}

func Read(root *os.Root, path string) (string, error) {
	cleaned, err := Clean(path)
	if err != nil || cleaned == "." {
		return "", errors.New("path is not inside the workspace")
	}
	if err := rejectLink(root, cleaned); err != nil {
		return "", err
	}
	file, err := root.Open(cleaned)
	if err != nil {
		return "", errors.New("path is not inside the workspace")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("path is not a readable file")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", errors.New("path is not a readable file")
	}
	if len(body) > maxBytes {
		return "", errors.New("file exceeds the size limit")
	}
	return string(body), nil
}

func rejectLink(root *os.Root, path string) error {
	if path == "." {
		return nil
	}
	info, err := root.Lstat(path)
	if err != nil {
		return errors.New("path is not inside the workspace")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlinks are not readable")
	}
	return nil
}
