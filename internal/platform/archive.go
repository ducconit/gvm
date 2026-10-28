package platform

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxDecompressedFileSize caps individual file extraction at 1 GiB to guard against zip/tar bombs.
	maxDecompressedFileSize = 1 << 30
)

// ExtractArchive extracts an official Go distribution archive (.tar.gz or .zip) into destDir.
// Official Go archives have a top-level "go/" directory; ExtractArchive strips the leading "go/"
// component so that destDir directly contains "bin/", "pkg/", "src/", etc.
// All archive paths and symlink targets are strictly validated against path traversal (Tar-Slip / Zip-Slip).
func ExtractArchive(ctx context.Context, archivePath, destDir string) error {
	cleanDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("resolve destination dir: %w", err)
	}
	if err := os.MkdirAll(cleanDest, 0o755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}

	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(ctx, archivePath, cleanDest)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(ctx, archivePath, cleanDest)
	default:
		return fmt.Errorf("unsupported archive format: %s", filepath.Base(archivePath))
	}
}

// sanitizeArchivePath strips an optional top-level "go/" prefix and verifies the resulting path stays strictly within destDir.
func sanitizeArchivePath(destDir, rawEntryName string) (string, error) {
	normalized := strings.ReplaceAll(rawEntryName, "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
		return "", fmt.Errorf("illegal absolute archive path: %q", rawEntryName)
	}

	// Check raw segments for ".." before cleaning
	for _, seg := range strings.Split(normalized, "/") {
		if seg == ".." {
			return "", fmt.Errorf("path traversal detected in archive entry: %q", rawEntryName)
		}
	}

	// Strip top-level "go/" prefix used in official Go toolchains
	if normalized == "go" || normalized == "go/" {
		return "", nil
	}
	normalized = strings.TrimPrefix(normalized, "go/")

	relPath := filepath.FromSlash(normalized)
	if relPath == "" || relPath == "." {
		return "", nil
	}

	targetPath := filepath.Join(destDir, relPath)
	relToDest, err := filepath.Rel(destDir, targetPath)
	if err != nil || strings.HasPrefix(relToDest, "..") || filepath.IsAbs(relToDest) {
		return "", fmt.Errorf("path traversal outside destination in archive entry: %q", rawEntryName)
	}
	return targetPath, nil
}

func validateSymlinkTarget(destDir, linkPath, linkTarget string) error {
	if filepath.IsAbs(linkTarget) || strings.HasPrefix(linkTarget, "/") || strings.HasPrefix(linkTarget, "\\") {
		return fmt.Errorf("illegal absolute symlink target %q in archive", linkTarget)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), filepath.FromSlash(linkTarget)))
	rel, err := filepath.Rel(destDir, resolved)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("symlink target %q escapes destination directory", linkTarget)
	}
	return nil
}

func extractTarGz(ctx context.Context, archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		targetPath, err := sanitizeArchivePath(destDir, hdr.Name)
		if err != nil {
			return err
		}
		if targetPath == "" {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", targetPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", targetPath, err)
			}
			mode := os.FileMode(hdr.Mode) & 0o777
			if mode == 0 {
				mode = 0o644
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("create file %s: %w", targetPath, err)
			}
			limited := io.LimitReader(tr, maxDecompressedFileSize+1)
			written, copyErr := io.Copy(outFile, limited)
			closeErr := outFile.Close()
			if copyErr != nil {
				return fmt.Errorf("extract file %s: %w", targetPath, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close file %s: %w", targetPath, closeErr)
			}
			if written > maxDecompressedFileSize {
				return fmt.Errorf("file %s exceeds maximum allowed size", hdr.Name)
			}
		case tar.TypeSymlink:
			if err := validateSymlinkTarget(destDir, targetPath, hdr.Linkname); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("mkdir symlink parent %s: %w", targetPath, err)
			}
			_ = os.Remove(targetPath)
			if err := os.Symlink(filepath.FromSlash(hdr.Linkname), targetPath); err != nil {
				return fmt.Errorf("create symlink %s: %w", targetPath, err)
			}
		}
	}
	return nil
}

func extractZip(ctx context.Context, archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer zr.Close()

	for _, zf := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		targetPath, err := sanitizeArchivePath(destDir, zf.Name)
		if err != nil {
			return err
		}
		if targetPath == "" {
			continue
		}

		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", targetPath, err)
			}
			continue
		}

		if zf.Mode()&os.ModeSymlink != 0 {
			rc, err := zf.Open()
			if err != nil {
				return fmt.Errorf("open zip symlink %s: %w", zf.Name, err)
			}
			linkBytes, readErr := io.ReadAll(io.LimitReader(rc, 4096))
			_ = rc.Close()
			if readErr != nil {
				return fmt.Errorf("read zip symlink %s: %w", zf.Name, readErr)
			}
			linkTarget := string(linkBytes)
			if err := validateSymlinkTarget(destDir, targetPath, linkTarget); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			_ = os.Remove(targetPath)
			if err := os.Symlink(filepath.FromSlash(linkTarget), targetPath); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", targetPath, err)
		}

		mode := zf.Mode() & 0o777
		if mode == 0 {
			mode = 0o644
		}
		// Ensure binaries inside bin/ are executable on Unix
		if strings.Contains(filepath.ToSlash(targetPath), "/bin/") {
			mode |= 0o755
		}

		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %s: %w", zf.Name, err)
		}
		outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = rc.Close()
			return fmt.Errorf("create file %s: %w", targetPath, err)
		}
		written, copyErr := io.Copy(outFile, io.LimitReader(rc, maxDecompressedFileSize+1))
		_ = rc.Close()
		closeErr := outFile.Close()
		if copyErr != nil {
			return fmt.Errorf("extract zip entry %s: %w", zf.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close file %s: %w", targetPath, closeErr)
		}
		if written > maxDecompressedFileSize {
			return fmt.Errorf("zip entry %s exceeds maximum allowed size", zf.Name)
		}
	}
	return nil
}
