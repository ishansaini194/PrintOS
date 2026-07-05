// Package render normalizes any accepted upload into one clean, standardized
// PDF the agent can print reliably: non-PDFs are converted to PDF (LibreOffice /
// image tools), then every PDF is optimized with Ghostscript. Unsupported,
// oversized, or broken files are rejected with a clear error.
//
// System tools required on the cloud host:
//   - gs       (Ghostscript)  — PDF optimize
//   - soffice  (LibreOffice)  — Office/text/image → PDF
//   - heif-convert or convert (ImageMagick) — HEIC → JPG/PNG
package render

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxUploadBytes caps accepted uploads (50MB). Override with env
// PRINTOS_MAX_UPLOAD_BYTES (bytes).
const MaxUploadBytes = 50 << 20

var (
	// ErrUnsupported is returned for file types we don't (yet) process.
	ErrUnsupported = errors.New("unsupported file type")
	// ErrTooLarge is returned when the input exceeds the size limit.
	ErrTooLarge = errors.New("file too large")
	// ErrConvertFailed is returned when a conversion or optimize step fails
	// (includes corrupt or password-protected PDFs).
	ErrConvertFailed = errors.New("could not process file")
)

// convertTimeout bounds each external tool invocation.
const convertTimeout = 60 * time.Second

// officeExts are converted to PDF via LibreOffice headless.
var officeExts = map[string]bool{
	".docx": true, ".doc": true,
	".pptx": true, ".ppt": true,
	".xlsx": true, ".xls": true,
	".txt": true,
}

// imageExts are also handled by LibreOffice (it can lay an image onto a page).
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
}

// maxUploadBytes returns the effective limit, honoring the env override.
func maxUploadBytes() int64 {
	if v := os.Getenv("PRINTOS_MAX_UPLOAD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return MaxUploadBytes
}

// Normalize turns inputPath into one clean, optimized PDF. It returns the output
// path and a cleanup func that removes every temp file it created; the caller
// must call cleanup once done with the output. On any rejection it returns a
// sentinel error (ErrTooLarge / ErrUnsupported / ErrConvertFailed) and a no-op
// cleanup.
func Normalize(inputPath string) (outputPath string, cleanup func(), err error) {
	noop := func() {}

	info, err := os.Stat(inputPath)
	if err != nil {
		return "", noop, fmt.Errorf("%w: %v", ErrConvertFailed, err)
	}
	if info.Size() > maxUploadBytes() {
		return "", noop, ErrTooLarge
	}

	// A work dir holds all intermediate files; cleanup removes it wholesale.
	workDir, err := os.MkdirTemp("", "printos-render-*")
	if err != nil {
		return "", noop, fmt.Errorf("%w: %v", ErrConvertFailed, err)
	}
	clean := func() { os.RemoveAll(workDir) }

	kind, err := detectKind(inputPath)
	if err != nil {
		clean()
		return "", noop, err
	}

	// Route to a PDF, then optimize.
	var pdfPath string
	switch kind {
	case kindPDF:
		pdfPath = inputPath
	case kindOffice, kindImage:
		pdfPath, err = convertToPDF(inputPath, workDir)
	case kindHEIC:
		var img string
		if img, err = heicToImage(inputPath, workDir); err == nil {
			pdfPath, err = convertToPDF(img, workDir)
		}
	default:
		clean()
		return "", noop, ErrUnsupported
	}
	if err != nil {
		clean()
		return "", noop, err
	}

	out, err := optimizePDF(pdfPath, workDir)
	if err != nil {
		clean()
		return "", noop, err
	}
	return out, clean, nil
}

type kind int

const (
	kindUnknown kind = iota
	kindPDF
	kindOffice
	kindImage
	kindHEIC
)

// detectKind classifies the file by extension AND content sniffing — the
// extension alone is not trusted. A mismatch (e.g. a .pdf that isn't a PDF)
// is rejected as unsupported.
func detectKind(path string) (kind, error) {
	ext := strings.ToLower(filepath.Ext(path))

	sniff, err := sniffContentType(path)
	if err != nil {
		return kindUnknown, fmt.Errorf("%w: %v", ErrConvertFailed, err)
	}

	switch {
	case ext == ".pdf":
		if sniff != "application/pdf" {
			return kindUnknown, ErrUnsupported // extension lies about content
		}
		return kindPDF, nil
	case ext == ".heic":
		return kindHEIC, nil // http sniff can't ID HEIC; trust the extension here
	case imageExts[ext]:
		if !strings.HasPrefix(sniff, "image/") {
			return kindUnknown, ErrUnsupported
		}
		return kindImage, nil
	case officeExts[ext]:
		return kindOffice, nil
	default:
		return kindUnknown, ErrUnsupported
	}
}

// sniffContentType reads the first 512 bytes and uses net/http detection.
func sniffContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n]), nil
}

// convertToPDF runs LibreOffice headless to convert in → a PDF in outDir,
// returning the produced PDF path.
func convertToPDF(in, outDir string) (string, error) {
	if err := runTool("soffice",
		"--headless", "--convert-to", "pdf", "--outdir", outDir, in,
	); err != nil {
		return "", err
	}
	// LibreOffice writes <basename>.pdf into outDir.
	base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
	out := filepath.Join(outDir, base+".pdf")
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("%w: soffice produced no output", ErrConvertFailed)
	}
	return out, nil
}

// heicToImage converts a HEIC file to a JPG in outDir, returning its path.
func heicToImage(in, outDir string) (string, error) {
	out := filepath.Join(outDir, "heic.jpg")
	if err := runTool("heif-convert", in, out); err == nil {
		if _, statErr := os.Stat(out); statErr == nil {
			return out, nil
		}
	}
	// Fall back to ImageMagick if heif-convert is unavailable or failed.
	if err := runTool("convert", in, out); err != nil {
		return "", err
	}
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("%w: heic conversion produced no output", ErrConvertFailed)
	}
	return out, nil
}

// optimizePDF runs Ghostscript (/prepress, quality-first) to standardize the
// PDF. Ghostscript failing here also catches corrupt / password-protected PDFs.
func optimizePDF(in, outDir string) (string, error) {
	out := filepath.Join(outDir, "optimized.pdf")
	if err := runTool("gs",
		"-sDEVICE=pdfwrite",
		"-dCompatibilityLevel=1.4",
		"-dPDFSETTINGS=/prepress",
		"-dColorImageResolution=600",
		"-dMonoImageResolution=1200",
		"-dNOPAUSE", "-dQUIET", "-dBATCH",
		"-sOutputFile="+out, in,
	); err != nil {
		return "", err
	}
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("%w: ghostscript produced no output", ErrConvertFailed)
	}
	return out, nil
}

// runTool executes an external tool with a timeout, mapping any failure to
// ErrConvertFailed (with the tool's output for context).
func runTool(name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), convertTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	outErr, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s: %v: %s", ErrConvertFailed, name, err, strings.TrimSpace(string(outErr)))
	}
	return nil
}
