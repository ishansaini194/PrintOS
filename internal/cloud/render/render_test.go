package render

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeSamplePDF writes a minimal but valid 1-page PDF (correct xref offsets).
func writeSamplePDF(t *testing.T, path string) {
	t.Helper()
	writeSamplePDFPages(t, path, 1)
}

// writeSamplePDFPages writes a minimal but valid n-page PDF.
func writeSamplePDFPages(t *testing.T, path string, n int) {
	t.Helper()
	kids := make([]string, n)
	pages := make([]string, n)
	for i := 0; i < n; i++ {
		kids[i] = fmt.Sprintf("%d 0 R", 3+i)
		pages[i] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>"
	}
	objs := append([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n),
	}, pages...)
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", len(objs)+1)
	b.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatalf("write sample pdf: %v", err)
	}
}

func TestNormalizePDF(t *testing.T) {
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("ghostscript (gs) not installed; skipping")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	writeSamplePDF(t, in)

	out, cleanup, err := Normalize(in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatalf("output is not a PDF (first bytes: %q)", data[:min(8, len(data))])
	}

	// cleanup must remove the produced files.
	cleanup()
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("output still present after cleanup: %v", err)
	}
}

func TestPageCount(t *testing.T) {
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("pdfinfo not installed; skipping")
	}
	dir := t.TempDir()
	for _, n := range []int{1, 3} {
		path := filepath.Join(dir, fmt.Sprintf("sample%d.pdf", n))
		writeSamplePDFPages(t, path, n)
		got, err := PageCount(path)
		if err != nil {
			t.Fatalf("PageCount(%d pages): %v", n, err)
		}
		if got != n {
			t.Errorf("PageCount = %d, want %d", got, n)
		}
	}
}

func TestPageCountNotAPDF(t *testing.T) {
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("pdfinfo not installed; skipping")
	}
	path := filepath.Join(t.TempDir(), "junk.pdf")
	if err := os.WriteFile(path, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PageCount(path); err == nil {
		t.Error("expected error for non-PDF input")
	}
}

func TestNormalizeTooLarge(t *testing.T) {
	t.Setenv("PRINTOS_MAX_UPLOAD_BYTES", "10")
	dir := t.TempDir()
	in := filepath.Join(dir, "big.pdf")
	if err := os.WriteFile(in, []byte("this content is definitely more than ten bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := Normalize(in)
	if err != ErrTooLarge {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	cleanup() // must be a safe no-op on error
}

func TestNormalizeUnsupported(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "file.xyz")
	if err := os.WriteFile(in, []byte("random bytes not a known type"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := Normalize(in)
	if err != ErrUnsupported {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	cleanup()
}
