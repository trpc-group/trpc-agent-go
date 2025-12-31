//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	cacheAppDir    = "trpc-agent-go"
	cacheSkillsDir = "skills"

	cacheReadyFile    = ".ready"
	cacheDownloadFile = "download"
	cacheExtractDir   = "root"

	cacheTempPrefix = "tmp-skill-root-"

	dirPerm  = 0o755
	filePerm = 0o644

	bytesPerMiB = 1 << 20

	maxDownloadBytes = 64 * bytesPerMiB

	maxExtractFileBytes  = 64 * bytesPerMiB
	maxExtractTotalBytes = 256 * bytesPerMiB
)

// EnvSkillsCacheDir overrides where URL-based skills roots are cached.
// When empty, the user cache directory is used.
const EnvSkillsCacheDir = "SKILLS_CACHE_DIR"

func resolveSkillsRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil
	}
	if !strings.Contains(root, "://") {
		return root, nil
	}
	u, err := url.Parse(root)
	if err != nil {
		return "", fmt.Errorf("parse skills root URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		return cacheURLRoot(u)
	case "file":
		return fileURLPath(u)
	default:
		return "", fmt.Errorf("unsupported skills root URL: %s", root)
	}
}

func fileURLPath(u *url.URL) (string, error) {
	if u == nil {
		return "", fmt.Errorf("nil file URL")
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("unsupported file URL host: %q", u.Host)
	}
	return filepath.FromSlash(u.Path), nil
}

func cacheURLRoot(u *url.URL) (string, error) {
	cacheDir := skillsCacheDir()
	key := sha256Hex(u.String())
	destDir := filepath.Join(cacheDir, key)
	ready := filepath.Join(destDir, cacheReadyFile)
	if fileExists(ready) {
		return destDir, nil
	}
	if err := os.RemoveAll(destDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cacheDir, dirPerm); err != nil {
		return "", err
	}
	tmpDir, err := os.MkdirTemp(cacheDir, cacheTempPrefix)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	srcPath := filepath.Join(tmpDir, cacheDownloadFile)
	if err := downloadURLToFile(u, srcPath); err != nil {
		return "", err
	}
	extractDir := filepath.Join(tmpDir, cacheExtractDir)
	if err := os.MkdirAll(extractDir, dirPerm); err != nil {
		return "", err
	}
	if err := extractURLPayload(u, srcPath, extractDir); err != nil {
		return "", err
	}
	if err := os.WriteFile(
		filepath.Join(extractDir, cacheReadyFile),
		[]byte("ok"),
		filePerm,
	); err != nil {
		return "", err
	}
	if err := os.Rename(extractDir, destDir); err != nil {
		if fileExists(ready) {
			return destDir, nil
		}
		return "", err
	}
	return destDir, nil
}

func skillsCacheDir() string {
	if d := strings.TrimSpace(os.Getenv(EnvSkillsCacheDir)); d != "" {
		return d
	}
	uc, err := os.UserCacheDir()
	if err == nil && uc != "" {
		return filepath.Join(uc, cacheAppDir, cacheSkillsDir)
	}
	return filepath.Join(os.TempDir(), cacheAppDir, cacheSkillsDir)
}

func downloadURLToFile(u *url.URL, path string) error {
	resp, err := http.Get(u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download skills root: %s", resp.Status)
	}
	if resp.ContentLength > maxDownloadBytes {
		return fmt.Errorf("download skills root: too large")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	lr := io.LimitReader(resp.Body, maxDownloadBytes+1)
	n, err := io.Copy(f, lr)
	if err != nil {
		return err
	}
	if n > maxDownloadBytes {
		return fmt.Errorf("download skills root: too large")
	}
	return nil
}

func extractURLPayload(u *url.URL, srcPath string, destDir string) error {
	name := strings.ToLower(path.Base(u.Path))
	kind := archiveKindFromName(name)
	if kind == archiveKindUnknown {
		kind = detectArchiveKind(srcPath)
	}
	switch kind {
	case archiveKindZip:
		return extractZip(srcPath, destDir)
	case archiveKindTar:
		return extractTar(srcPath, destDir)
	case archiveKindTarGZ:
		return extractTarGZ(srcPath, destDir)
	default:
		if strings.EqualFold(path.Base(u.Path), skillFile) {
			return writeSingleSkillFile(srcPath, destDir)
		}
		return fmt.Errorf("unsupported skills root file: %s", u.Path)
	}
}

type archiveKind int

const (
	archiveKindUnknown archiveKind = iota
	archiveKindZip
	archiveKindTar
	archiveKindTarGZ
)

const (
	extZip   = ".zip"
	extTar   = ".tar"
	extTGZ   = ".tgz"
	extTarGZ = ".tar.gz"
)

func archiveKindFromName(name string) archiveKind {
	switch {
	case strings.HasSuffix(name, extZip):
		return archiveKindZip
	case strings.HasSuffix(name, extTarGZ) || strings.HasSuffix(name, extTGZ):
		return archiveKindTarGZ
	case strings.HasSuffix(name, extTar):
		return archiveKindTar
	default:
		return archiveKindUnknown
	}
}

func detectArchiveKind(srcPath string) archiveKind {
	f, err := os.Open(srcPath)
	if err != nil {
		return archiveKindUnknown
	}
	defer f.Close()
	var hdr [4]byte
	n, _ := io.ReadFull(f, hdr[:])
	if n >= 4 && string(hdr[:4]) == "PK\x03\x04" {
		return archiveKindZip
	}
	if n >= 2 && hdr[0] == 0x1f && hdr[1] == 0x8b {
		return archiveKindTarGZ
	}
	return archiveKindUnknown
}

func extractZip(srcPath string, destDir string) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	var total int64
	for _, f := range zr.File {
		if err := extractZipFile(f, destDir, &total); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, destDir string, total *int64) error {
	if f == nil {
		return fmt.Errorf("nil zip entry")
	}
	clean, err := cleanArchivePath(f.Name)
	if err != nil {
		return err
	}
	if clean == "" {
		return nil
	}
	info := f.FileInfo()
	if info.Mode()&os.ModeType != 0 && !info.IsDir() {
		return fmt.Errorf("unsupported zip entry: %q", f.Name)
	}
	target := filepath.Join(destDir, filepath.FromSlash(clean))
	if info.IsDir() {
		return os.MkdirAll(target, dirPerm)
	}
	if err := validateZipEntrySize(f); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return err
	}
	mode := sanitizePerm(info.Mode().Perm())
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(
		target,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
		mode,
	)
	if err != nil {
		return err
	}
	defer out.Close()
	lr := io.LimitReader(rc, maxExtractFileBytes+1)
	n, err := io.Copy(out, lr)
	if err != nil {
		return err
	}
	if n > maxExtractFileBytes {
		return fmt.Errorf("zip entry too large: %q", f.Name)
	}
	if err := addExtractedBytes(total, n); err != nil {
		return err
	}
	return nil
}

func extractTar(srcPath string, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return extractTarReader(tar.NewReader(f), destDir)
}

func extractTarGZ(srcPath string, destDir string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	return extractTarReader(tar.NewReader(gz), destDir)
}

func extractTarReader(tr *tar.Reader, destDir string) error {
	var total int64
	for {
		h, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		clean, err := cleanArchivePath(h.Name)
		if err != nil {
			return err
		}
		if clean == "" {
			continue
		}
		target := filepath.Join(destDir, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, dirPerm); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
				return err
			}
			if err := validateTarSize(h.Size); err != nil {
				return err
			}
			if err := addExtractedBytes(&total, h.Size); err != nil {
				return err
			}
			mode := tarHeaderPerm(h.Mode)
			out, err := os.OpenFile(
				target,
				os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
				mode,
			)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(out, tr, h.Size); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar entry: %q", h.Name)
		}
	}
}

func writeSingleSkillFile(srcPath string, destDir string) error {
	st, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if st.Size() > maxExtractFileBytes {
		return fmt.Errorf("skill file too large")
	}
	b, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, skillFile), b, filePerm)
}

func cleanArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Clean(name)
	if name == "." {
		return "", nil
	}
	if strings.HasPrefix(name, "/") || name == ".." ||
		strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("invalid archive path: %q", name)
	}
	if strings.Contains(name, ":") {
		return "", fmt.Errorf("invalid archive path: %q", name)
	}
	return strings.TrimPrefix(name, "./"), nil
}

func sanitizePerm(m os.FileMode) os.FileMode {
	if m == 0 {
		return filePerm
	}
	return m & 0o777
}

const (
	tarPermUserRead  = 0o400
	tarPermUserWrite = 0o200
	tarPermUserExec  = 0o100

	tarPermGroupRead  = 0o040
	tarPermGroupWrite = 0o020
	tarPermGroupExec  = 0o010

	tarPermOtherRead  = 0o004
	tarPermOtherWrite = 0o002
	tarPermOtherExec  = 0o001
)

func tarHeaderPerm(mode int64) os.FileMode {
	if mode < 0 {
		return filePerm
	}
	var perm os.FileMode
	if mode&tarPermUserRead != 0 {
		perm |= tarPermUserRead
	}
	if mode&tarPermUserWrite != 0 {
		perm |= tarPermUserWrite
	}
	if mode&tarPermUserExec != 0 {
		perm |= tarPermUserExec
	}
	if mode&tarPermGroupRead != 0 {
		perm |= tarPermGroupRead
	}
	if mode&tarPermGroupWrite != 0 {
		perm |= tarPermGroupWrite
	}
	if mode&tarPermGroupExec != 0 {
		perm |= tarPermGroupExec
	}
	if mode&tarPermOtherRead != 0 {
		perm |= tarPermOtherRead
	}
	if mode&tarPermOtherWrite != 0 {
		perm |= tarPermOtherWrite
	}
	if mode&tarPermOtherExec != 0 {
		perm |= tarPermOtherExec
	}
	return sanitizePerm(perm)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func validateTarSize(size int64) error {
	if size < 0 {
		return fmt.Errorf("tar entry has negative size")
	}
	if size > maxExtractFileBytes {
		return fmt.Errorf("tar entry too large")
	}
	return nil
}

func validateZipEntrySize(f *zip.File) error {
	if f == nil {
		return fmt.Errorf("nil zip entry")
	}
	if f.UncompressedSize64 > uint64(maxExtractFileBytes) {
		return fmt.Errorf("zip entry too large: %q", f.Name)
	}
	return nil
}

func addExtractedBytes(total *int64, n int64) error {
	if total == nil {
		return nil
	}
	if n < 0 {
		return fmt.Errorf("negative extract size")
	}
	if *total > maxExtractTotalBytes-n {
		return fmt.Errorf("skills root archive too large")
	}
	*total += n
	return nil
}
