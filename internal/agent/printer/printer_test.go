package printer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// fakeSumatra writes a script that exits with the given code, standing in for
// SumatraPDF.exe so tests need no real printer.
func fakeSumatra(t *testing.T, exitCode int) string {
	t.Helper()
	dir := t.TempDir()

	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "sumatra.bat")
		script := "@echo off\r\nexit /b " + itoa(exitCode) + "\r\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	path := filepath.Join(dir, "sumatra.sh")
	script := "#!/bin/sh\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

func TestPrintSuccess(t *testing.T) {
	p := New(fakeSumatra(t, 0))
	state, err := p.Print("dummy.pdf", "SomePrinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != protocol.StateDone {
		t.Errorf("expected done, got %s", state)
	}
}

func TestPrintFailure(t *testing.T) {
	p := New(fakeSumatra(t, 1))
	state, err := p.Print("dummy.pdf", "SomePrinter")
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if state != protocol.StateFailed {
		t.Errorf("expected failed, got %s", state)
	}
}
