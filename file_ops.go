package main

import (
	"archive/zip"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

func loadIgnoreMatcher(cfg Config) *IgnoreMatcher {
	return loadBackupIgnoreMatcherForTarget(cfg.TargetDir, cfg.BackupIgnore)
}

func loadBackupIgnoreMatcherForTarget(targetDir string, backupIgnore []string) *IgnoreMatcher {
	patterns := append([]string{}, backupIgnore...)
	extra := readIgnoreFilePatterns(filepath.Join(targetDir, ".updateignore"))
	patterns = append(patterns, extra...)
	patterns = append(patterns, ".updateignore")
	return newIgnoreMatcher(patterns)
}

func loadReplaceIgnoreMatcher(cfg Config) *IgnoreMatcher {
	patterns := append([]string{}, resolveReplaceIgnoreRulesForTarget(cfg.TargetDir, cfg.ReplaceIgnore, cfg.BackupIgnore)...)
	patterns = append(patterns, ".replaceignore")
	return newIgnoreMatcher(patterns)
}

func resolveReplaceIgnoreRulesForTarget(targetDir string, replaceIgnore, backupIgnore []string) []string {
	patterns := append([]string{}, replaceIgnore...)
	extra := readIgnoreFilePatterns(filepath.Join(targetDir, ".replaceignore"))
	patterns = append(patterns, extra...)
	if len(patterns) == 0 {
		patterns = append(patterns, backupIgnore...)
	}
	return uniquePatterns(patterns)
}

func readIgnoreFilePatterns(file string) []string {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(b), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func uniquePatterns(patterns []string) []string {
	seen := make(map[string]struct{}, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func newIgnoreMatcher(patterns []string) *IgnoreMatcher {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = normalizeRelPath(p)
		if p == "" || p == "." {
			continue
		}
		out = append(out, p)
	}
	return &IgnoreMatcher{patterns: out}
}

func (m *IgnoreMatcher) ShouldIgnore(rel string, isDir bool) bool {
	rel = normalizeRelPath(rel)
	if rel == "" || rel == "." {
		return false
	}
	for _, raw := range m.patterns {
		p := strings.TrimPrefix(raw, "/")
		if strings.HasSuffix(p, "/") {
			base := strings.TrimSuffix(p, "/")
			if rel == base || strings.HasPrefix(rel, base+"/") {
				return true
			}
			continue
		}
		if strings.ContainsAny(p, "*?[]") {
			if ok, _ := pathpkg.Match(p, rel); ok {
				return true
			}
			if ok, _ := pathpkg.Match(p, pathpkg.Base(rel)); ok {
				return true
			}
			continue
		}
		if rel == p {
			return true
		}
		if isDir && strings.HasPrefix(p, rel+"/") {
			return false
		}
		if strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

func zipDirectory(srcDir, dstZip string, ignore *IgnoreMatcher) error {
	if err := os.MkdirAll(filepath.Dir(dstZip), 0755); err != nil {
		return err
	}
	f, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == srcDir {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		rel = normalizeRelPath(rel)
		if ignore.ShouldIgnore(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
}

func extractZip(srcZip, dstDir string) error {
	r, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer r.Close()

	base := filepath.Clean(dstDir) + string(os.PathSeparator)
	for _, f := range r.File {
		name := normalizeRelPath(f.Name)
		if name == "" || name == "." {
			continue
		}
		destPath := filepath.Join(dstDir, filepath.FromSlash(name))
		cleanDest := filepath.Clean(destPath)
		if !strings.HasPrefix(cleanDest, base) && cleanDest != filepath.Clean(dstDir) {
			return fmt.Errorf("zip 非法路径: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
		if err != nil {
			src.Close()
			return err
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		srcErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if srcErr != nil {
			return srcErr
		}
	}
	return nil
}

func syncDirectories(src, target string, ignore *IgnoreMatcher, removeMissing bool) ([]ChangedFile, error) {
	type srcFile struct {
		abs  string
		size int64
	}
	sourceFiles := make(map[string]srcFile)
	sourceDirs := map[string]struct{}{"": {}}

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		rel = normalizeRelPath(rel)
		if ignore.ShouldIgnore(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			sourceDirs[rel] = struct{}{}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sourceFiles[rel] = srcFile{abs: path, size: info.Size()}
		sourceDirs[pathpkg.Dir(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}

	changes := make([]ChangedFile, 0)
	targetDirs := make([]string, 0)

	if removeMissing {
		err = filepath.WalkDir(target, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == target {
				return nil
			}
			rel, err := filepath.Rel(target, path)
			if err != nil {
				return err
			}
			rel = normalizeRelPath(rel)
			if ignore.ShouldIgnore(rel, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if _, ok := sourceDirs[rel]; !ok {
					targetDirs = append(targetDirs, path)
				}
				return nil
			}
			if _, ok := sourceFiles[rel]; !ok {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				changes = append(changes, ChangedFile{Path: rel, Action: "deleted", Size: 0})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}

		sort.Slice(targetDirs, func(i, j int) bool { return len(targetDirs[i]) > len(targetDirs[j]) })
		for _, d := range targetDirs {
			_ = os.Remove(d)
		}
	}

	keys := make([]string, 0, len(sourceFiles))
	for k := range sourceFiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, rel := range keys {
		sf := sourceFiles[rel]
		dst := filepath.Join(target, filepath.FromSlash(rel))
		exists := true
		if _, err := os.Stat(dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				exists = false
			} else {
				return nil, err
			}
		}
		same, err := filesEqual(sf.abs, dst)
		if err != nil {
			return nil, err
		}
		if same {
			continue
		}
		if err := copyFile(sf.abs, dst); err != nil {
			return nil, err
		}
		action := "added"
		if exists {
			action = "updated"
		}
		changes = append(changes, ChangedFile{Path: rel, Action: action, Size: sf.size})
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

func previewDirectoryChanges(src, target string, ignore *IgnoreMatcher, removeMissing bool) ([]ChangedFile, []string, error) {
	type srcFile struct {
		abs  string
		size int64
	}
	sourceFiles := make(map[string]srcFile)
	ignoredSet := make(map[string]struct{})
	addIgnored := func(rel string) {
		rel = normalizeRelPath(rel)
		if rel == "" {
			return
		}
		ignoredSet[rel] = struct{}{}
	}

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		rel = normalizeRelPath(rel)
		if ignore.ShouldIgnore(rel, d.IsDir()) {
			addIgnored(rel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sourceFiles[rel] = srcFile{abs: path, size: info.Size()}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	changes := make([]ChangedFile, 0)
	if removeMissing {
		err = filepath.WalkDir(target, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == target {
				return nil
			}
			rel, err := filepath.Rel(target, path)
			if err != nil {
				return err
			}
			rel = normalizeRelPath(rel)
			if ignore.ShouldIgnore(rel, d.IsDir()) {
				addIgnored(rel)
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if _, ok := sourceFiles[rel]; !ok {
				changes = append(changes, ChangedFile{Path: rel, Action: "deleted", Size: 0})
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	keys := make([]string, 0, len(sourceFiles))
	for k := range sourceFiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, rel := range keys {
		sf := sourceFiles[rel]
		dst := filepath.Join(target, filepath.FromSlash(rel))
		exists := true
		if _, err := os.Stat(dst); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				exists = false
			} else {
				return nil, nil, err
			}
		}
		same, err := filesEqual(sf.abs, dst)
		if err != nil {
			return nil, nil, err
		}
		if same {
			continue
		}
		action := "added"
		if exists {
			action = "updated"
		}
		changes = append(changes, ChangedFile{Path: rel, Action: action, Size: sf.size})
	}

	ignoredPaths := make([]string, 0, len(ignoredSet))
	for rel := range ignoredSet {
		ignoredPaths = append(ignoredPaths, rel)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	sort.Strings(ignoredPaths)
	return changes, ignoredPaths, nil
}

func clearDirWithIgnore(target string, ignore *IgnoreMatcher) error {
	type entry struct {
		path  string
		isDir bool
	}
	entries := make([]entry, 0)

	err := filepath.WalkDir(target, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == target {
			return nil
		}
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return err
		}
		rel = normalizeRelPath(rel)
		if ignore.ShouldIgnore(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entries = append(entries, entry{path: path, isDir: d.IsDir()})
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(entries, func(i, j int) bool { return len(entries[i].path) > len(entries[j].path) })
	for _, e := range entries {
		if e.isDir {
			_ = os.Remove(e.path)
			continue
		}
		if err := os.Remove(e.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func saveMultipartFile(src multipart.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

func filesEqual(a, b string) (bool, error) {
	statA, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	statB, err := os.Stat(b)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if statA.Size() != statB.Size() {
		return false, nil
	}

	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()

	const chunk = 32 * 1024
	ba := make([]byte, chunk)
	bb := make([]byte, chunk)
	for {
		na, ea := fa.Read(ba)
		nb, eb := fb.Read(bb)
		if na != nb {
			return false, nil
		}
		if na > 0 && subtle.ConstantTimeCompare(ba[:na], bb[:nb]) != 1 {
			return false, nil
		}
		if errors.Is(ea, io.EOF) && errors.Is(eb, io.EOF) {
			return true, nil
		}
		if ea != nil && !errors.Is(ea, io.EOF) {
			return false, ea
		}
		if eb != nil && !errors.Is(eb, io.EOF) {
			return false, eb
		}
	}
}

func normalizeRelPath(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\\", "/"))
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimPrefix(s, "/")
	s = pathpkg.Clean(s)
	if s == "." {
		return ""
	}
	return s
}
