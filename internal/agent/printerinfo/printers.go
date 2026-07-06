// Package printerinfo gives the agent knowledge of its printers and each
// one's type (mono/color). On first run it detects printer names, prompts the
// owner to tag each, and stores the result to a JSON file; later runs load
// that file and skip the prompt.
//
// This is step 1 of multi-printer support: detect + tag + store only. Routing,
// per-printer workers, and job types come later.
package printerinfo

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Printer is a detected printer tagged with its color capability.
type Printer struct {
	Name string `json:"name"`
	Type string `json:"type"` // "mono" | "color"
}

// String renders as "Name=Type" so a slice logs as [HP LaserJet=mono ...].
func (p Printer) String() string { return p.Name + "=" + p.Type }

// LoadOrTag returns the printers from path if it exists, otherwise it detects
// printers, prompts the owner to tag each (mono/color) on stdin, writes the
// result to path, and returns it.
func LoadOrTag(path string) ([]Printer, error) {
	return loadOrTag(path, os.Stdin, os.Stdout)
}

// loadOrTag is the testable core: I/O is injected so the prompt loop can be
// exercised without a real terminal.
func loadOrTag(path string, in io.Reader, out io.Writer) ([]Printer, error) {
	if data, err := os.ReadFile(path); err == nil {
		var ps []Printer
		if err := json.Unmarshal(data, &ps); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return ps, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	names, err := detectPrinterNames()
	if err != nil {
		return nil, fmt.Errorf("detect printers: %w", err)
	}

	reader := bufio.NewReader(in)
	ps := make([]Printer, 0, len(names))
	for _, name := range names {
		typ, err := promptType(reader, out, name)
		if err != nil {
			return nil, err
		}
		ps = append(ps, Printer{Name: name, Type: typ})
	}

	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return ps, nil
}

// promptType asks the owner whether name is mono or color, re-asking until a
// valid answer is given (case-insensitive).
func promptType(reader *bufio.Reader, out io.Writer, name string) (string, error) {
	for {
		fmt.Fprintf(out, "Detected printer: %q\n", name)
		fmt.Fprint(out, "Is this mono or color? [mono/color]: ")

		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read tag for %q: %w", name, err)
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "mono":
			return "mono", nil
		case "color":
			return "color", nil
		default:
			fmt.Fprintln(out, `Please type "mono" or "color".`)
		}
	}
}

// detectPrinterNames returns installed printer names. On Windows it queries via
// PowerShell; on other platforms (dev hosts) it returns a stub list so the
// detect→tag→store flow is exercisable off Windows.
func detectPrinterNames() ([]string, error) {
	if runtime.GOOS != "windows" {
		return []string{"Dev Printer A", "Dev Printer B"}, nil
	}

	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-Printer | Select-Object -ExpandProperty Name")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if n := strings.TrimSpace(line); n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}
