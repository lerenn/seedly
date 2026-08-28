package download

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent"
)

// WriteContent streams a completed torrent's files to the response.
// Single file: raw file. Multiple files: streamed zip.
func WriteContent(w http.ResponseWriter, tt *torrent.Torrent, dataDir string) error {
	if tt == nil || tt.Info() == nil {
		return fmt.Errorf("torrent not ready")
	}
	if tt.BytesCompleted() < tt.Length() {
		return fmt.Errorf("torrent incomplete")
	}

	files := tt.Files()
	if len(files) == 0 {
		return fmt.Errorf("no files")
	}

	if len(files) == 1 {
		f := files[0]
		return writeRelativeFile(w, dataDir, f.Path(), filepath.Base(f.Path()))
	}

	zipName := sanitizeFilename(tt.Name()) + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipName))

	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, f := range files {
		if err := addRelativeFileToZip(zw, dataDir, f.Path()); err != nil {
			return err
		}
	}
	return nil
}

// WriteFromDisk walks dataDir when live torrent handle is unavailable (paused but complete).
func WriteFromDisk(w http.ResponseWriter, name, dataDir string) error {
	var rels []string
	err := filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		if _, err := safeJoin(dataDir, rel); err != nil {
			return err
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return err
	}
	if len(rels) == 0 {
		return fmt.Errorf("no files")
	}
	if len(rels) == 1 {
		return writeRelativeFile(w, dataDir, rels[0], filepath.Base(rels[0]))
	}

	zipName := sanitizeFilename(name) + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipName))
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, rel := range rels {
		if err := addRelativeFileToZip(zw, dataDir, rel); err != nil {
			return err
		}
	}
	return nil
}

func writeRelativeFile(w http.ResponseWriter, root, rel, name string) error {
	f, err := openUnderRoot(root, rel)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(name)))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", st.Size()))
	_, err = io.Copy(w, f)
	return err
}

func addRelativeFileToZip(zw *zip.Writer, root, rel string) error {
	f, err := openUnderRoot(root, rel)
	if err != nil {
		return err
	}
	defer f.Close()
	fw, err := zw.Create(filepath.ToSlash(rel))
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, f)
	return err
}

// openUnderRoot opens rel only after verifying it stays within root.
func openUnderRoot(root, rel string) (*os.File, error) {
	full, err := safeJoin(root, rel)
	if err != nil {
		return nil, err
	}
	return os.Open(full) //nolint:gosec // path constrained by safeJoin
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" {
		return "download"
	}
	return name
}

func safeJoin(root, rel string) (string, error) {
	cleanRoot := filepath.Clean(root)
	full := filepath.Clean(filepath.Join(cleanRoot, rel))
	sep := string(os.PathSeparator)
	if full != cleanRoot && !strings.HasPrefix(full, cleanRoot+sep) {
		return "", fmt.Errorf("path escapes data dir")
	}
	return full, nil
}
