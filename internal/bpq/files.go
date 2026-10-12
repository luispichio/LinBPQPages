package bpq

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func (s *Store) DecodedFiles() ([]FileGroup, error) {
	entries, err := os.ReadDir(s.filesPath)
	if errors.Is(err, os.ErrNotExist) {
		return []FileGroup{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("leer HTML/7plus: %w", err)
	}
	groups := make([]FileGroup, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !safeBaseName(name) || strings.EqualFold(name, "list.csv") || strings.EqualFold(name, "index.html") {
			continue
		}
		path := filepath.Join(s.filesPath, name)
		file, info, err := openRegularNoFollow(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ELOOP) {
				continue
			}
			return nil, fmt.Errorf("stat archivo 7+ %q: %w", name, err)
		}
		mimeType := detectMIME(file, info.Size())
		file.Close()
		decodedFile := FileInfo{
			Name:     name,
			Size:     info.Size(),
			MIME:     mimeType,
			Modified: info.ModTime().UTC(),
		}
		// El directorio de salida de LinBPQ contiene archivos independientes;
		// mantenerlos separados aunque compartan el nombre base.
		groups = append(groups, FileGroup{Name: name, Primary: &decodedFile})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

func (s *Store) ReadDecodedFile(name string) (*os.File, os.FileInfo, error) {
	if !safeBaseName(name) {
		return nil, nil, os.ErrInvalid
	}
	path := filepath.Join(s.filesPath, name)
	return openRegularNoFollow(path)
}

func safeBaseName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00")
}

func detectMIME(file *os.File, size int64) string {
	limit := int64(512)
	if size < limit {
		limit = size
	}
	buffer := make([]byte, limit)
	read, err := io.ReadFull(file, buffer)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "application/octet-stream"
	}
	if read == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(buffer[:read])
}

func openRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		syscall.Close(fd)
		return nil, nil, os.ErrInvalid
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, os.ErrInvalid
	}
	return file, info, nil
}
