package printerinfo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFirstRunPromptsAndWrites: no file → prompts per detected (stub) printer,
// writes printers.json, returns the tagged list.
func TestFirstRunPromptsAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printers.json")
	// Two stub printers → two answers.
	in := strings.NewReader("mono\ncolor\n")
	var out bytes.Buffer

	ps, err := loadOrTag(path, in, &out)
	if err != nil {
		t.Fatalf("loadOrTag: %v", err)
	}
	want := []Printer{
		{Name: "Dev Printer A", Type: "mono"},
		{Name: "Dev Printer B", Type: "color"},
	}
	if len(ps) != len(want) {
		t.Fatalf("got %d printers, want %d", len(ps), len(want))
	}
	for i := range want {
		if ps[i] != want[i] {
			t.Errorf("printer[%d] = %+v, want %+v", i, ps[i], want[i])
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("printers.json not written: %v", err)
	}
}

// TestSecondRunLoadsSilently: file exists → no prompt (empty stdin), same list.
func TestSecondRunLoadsSilently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printers.json")
	if _, err := loadOrTag(path, strings.NewReader("mono\ncolor\n"), &bytes.Buffer{}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	var out bytes.Buffer
	// Empty stdin: if it tried to prompt, ReadString would EOF and error.
	ps, err := loadOrTag(path, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d printers, want 2", len(ps))
	}
	if out.Len() != 0 {
		t.Errorf("second run prompted (output: %q); expected silent load", out.String())
	}
}

// TestInvalidInputReAsks: junk answers are rejected until a valid one is given.
func TestInvalidInputReAsks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "printers.json")
	// First printer: two bad answers then "COLOR" (case-insensitive); second: "mono".
	in := strings.NewReader("foo\n\nCOLOR\nmono\n")
	var out bytes.Buffer

	ps, err := loadOrTag(path, in, &out)
	if err != nil {
		t.Fatalf("loadOrTag: %v", err)
	}
	if ps[0].Type != "color" {
		t.Errorf("printer[0] type = %q, want color", ps[0].Type)
	}
	if ps[1].Type != "mono" {
		t.Errorf("printer[1] type = %q, want mono", ps[1].Type)
	}
	if strings.Count(out.String(), `"mono" or "color"`) != 2 {
		t.Errorf("expected 2 re-ask messages, got output: %q", out.String())
	}
}

func TestDetectStubOnNonWindows(t *testing.T) {
	names, err := detectPrinterNames()
	if err != nil {
		t.Fatalf("detectPrinterNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one detected printer name")
	}
}
