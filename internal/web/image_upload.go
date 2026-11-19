package web

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxUploadSize caps a single image upload — generous for a character
// portrait or a lorebook cover, not so generous that a confused 50MB PNG
// export silently eats disk space.
const maxUploadSize = 10 << 20 // 10 MiB

// allowedImageExt maps a sniffed content-type to the extension saved to
// disk. net/http.DetectContentType sniffs the actual bytes rather than
// trusting the browser-supplied filename, so a renamed .exe can't ride
// in as "image.png".
var allowedImageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

func saveUploadedImage(w http.ResponseWriter, r *http.Request, formField, subdir string, entityID int64) (string, error) {
	// ParseMultipartForm's own maxMemory argument only bounds how much
	// gets buffered in RAM before spilling to a temp file — it does
	// NOT cap the request body's total size, so without this a 25MiB+
	// upload sails through and lands on disk anyway (confirmed by the
	// security review). MaxBytesReader makes the body itself refuse to
	// yield more than maxUploadSize bytes, so ParseMultipartForm fails
	// outright once the limit is crossed.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		return "", fmt.Errorf("parsing multipart form: %w", err)
	}
	file, header, err := r.FormFile(formField)
	if err == http.ErrMissingFile {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading upload: %w", err)
	}
	defer file.Close()
	_ = header // filename itself is untrusted/unused — ext comes from sniffing, name from entityID
	return writeImageToDisk(file, subdir, entityID)
}

// saveImageBytes is saveUploadedImage's counterpart for an image that
// didn't arrive as a multipart form field — specifically, the PNG an
// import uploaded, reused as the new character/lorebook's own image
// (see handlers_import.go) rather than leaving it unset just because
// the bytes came from a Character Card PNG instead of a plain
// <input type=file name=image> post.
func saveImageBytes(raw []byte, subdir string, entityID int64) (string, error) {
	return writeImageToDisk(bytes.NewReader(raw), subdir, entityID)
}

// writeImageToDisk sniffs src's real content type, rejects anything
// that isn't an allowed image format, and writes it to
// web/static/uploads/<subdir>/<entityID><ext>, overwriting any
// previous upload for that entity (one image per card/lorebook, not
// a gallery). Returns the path relative to web/static plus a
// cache-busting query string — e.g.
// "uploads/characters/3.png?v=1700000000000000000" — ready to store
// directly in the image_path column and to prefix with "/static/"
// for an <img src>. The query string matters: re-uploading onto the
// same entity ID keeps the same filename, so without it the
// browser's HTTP cache serves the OLD image forever after a replace
// — what looked like "nothing happened" when swapping a character's
// portrait was this, not a save failure (confirmed: the file on disk
// did change).
func writeImageToDisk(src io.ReadSeeker, subdir string, entityID int64) (string, error) {
	ext, err := sniffImageExt(src)
	if err != nil {
		return "", err
	}

	dir := filepath.Join("web", "static", "uploads", subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating upload dir: %w", err)
	}

	// A prior upload for this entity may have landed with a different
	// extension (.png then .jpg, say) — same-extension overwrite below
	// wouldn't touch that leftover file, which would otherwise rot on
	// disk forever as an orphan nothing points to anymore.
	removeExistingUploads(dir, entityID)

	diskPath := filepath.Join(dir, fmt.Sprintf("%d%s", entityID, ext))
	out, err := os.Create(diskPath)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", diskPath, err)
	}
	defer out.Close()

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewinding upload: %w", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		return "", fmt.Errorf("writing %s: %w", diskPath, err)
	}

	relPath := filepath.ToSlash(filepath.Join("uploads", subdir, fmt.Sprintf("%d%s", entityID, ext)))
	return fmt.Sprintf("%s?v=%d", relPath, time.Now().UnixNano()), nil
}

// removeExistingUploads deletes any file already named "<entityID>.*" in
// dir. Errors are ignored — a missing file (the common case: nothing
// uploaded yet) isn't a problem worth failing the new upload over.
func removeExistingUploads(dir string, entityID int64) {
	matches, _ := filepath.Glob(filepath.Join(dir, fmt.Sprintf("%d.*", entityID)))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// removeUploadedFile deletes the on-disk file behind a stored
// image_path value — which may carry the "?v=..." cache-busting query
// appended by saveUploadedImage, so that suffix is stripped first. A
// missing file (already gone, or path was "" meaning no image) isn't
// treated as an error: the goal is "don't leave an orphan behind when
// one exists", not "guarantee one existed".
func removeUploadedFile(relPath string) {
	if relPath == "" {
		return
	}
	if i := strings.IndexByte(relPath, '?'); i != -1 {
		relPath = relPath[:i]
	}
	_ = os.Remove(filepath.Join("web", "static", relPath))
}

func sniffImageExt(file io.ReadSeeker) (string, error) {
	head := make([]byte, 512)
	n, err := file.Read(head)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("reading upload header: %w", err)
	}
	contentType := http.DetectContentType(head[:n])
	ext, ok := allowedImageExt[contentType]
	if !ok {
		return "", fmt.Errorf("unsupported image type %q (png/jpeg/gif/webp only)", contentType)
	}
	return ext, nil
}
