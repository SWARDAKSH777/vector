package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func safeRootCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"HOME=/root",
		"PYTHONNOUSERSITE=1",
		"PYTHONDONTWRITEBYTECODE=1",
	}
	return cmd
}

func validateManagedRegularFile(path string, marker []byte) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("refusing unsafe managed path %s", path)
	}
	if info.Size() > 1<<20 {
		return nil, false, fmt.Errorf("managed file is unexpectedly large: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if len(marker) > 0 && !bytes.Contains(data, marker) {
		return nil, false, fmt.Errorf("refusing to replace a file not managed by Vector: %s", path)
	}
	return data, true, nil
}

func nginxNameConflicts(domain string, wildcard bool, targetPath string) error {
	roots := []string{"/etc/nginx/conf.d", "/etc/nginx/sites-enabled"}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect nginx configuration: %w", err)
		}
		for _, entry := range entries {
			path := filepath.Join(root, entry.Name())
			if filepath.Clean(path) == filepath.Clean(targetPath) {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			for _, name := range extractServerNames(data) {
				if serverNameOverlaps(name, domain, wildcard) {
					return fmt.Errorf("nginx server_name %q in %s conflicts with %s; remove the conflict before provisioning", name, path, domain)
				}
			}
		}
	}
	return nil
}

func extractServerNames(data []byte) []string {
	var names []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var pending string
	for scanner.Scan() {
		line := scanner.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if pending != "" {
			line = pending + " " + line
		}
		idx := strings.Index(line, "server_name")
		if idx < 0 {
			pending = ""
			continue
		}
		fragment := strings.TrimSpace(line[idx+len("server_name"):])
		if !strings.Contains(fragment, ";") {
			pending = "server_name " + fragment
			continue
		}
		pending = ""
		fragment = strings.SplitN(fragment, ";", 2)[0]
		for _, item := range strings.Fields(fragment) {
			item = strings.ToLower(strings.TrimSpace(item))
			if item != "" {
				names = append(names, item)
			}
		}
	}
	return names
}

func serverNameOverlaps(existing, domain string, wildcard bool) bool {
	existing = strings.ToLower(strings.TrimSpace(existing))
	domain = strings.ToLower(domain)
	if existing == "_" || existing == "" {
		return false
	}
	if strings.HasPrefix(existing, "~") {
		// Regex names cannot be analyzed safely; refuse rather than risk hijacking.
		return true
	}
	if existing == domain || existing == "*."+domain || existing == "."+domain {
		return true
	}
	if wildcard && strings.HasSuffix(existing, "."+domain) {
		return true
	}
	if strings.HasPrefix(existing, "*.") {
		base := strings.TrimPrefix(existing, "*.")
		return domain == base || strings.HasSuffix(domain, "."+base) || (wildcard && strings.HasSuffix(base, "."+domain))
	}
	return false
}

func rejectSymlinkDirectory(path string, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, mode)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing unsafe directory %s", path)
	}
	return os.Chmod(path, mode)
}
