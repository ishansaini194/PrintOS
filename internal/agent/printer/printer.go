// Package printer silently prints a PDF via SumatraPDF.
//
// v1 keeps result detection simple: if SumatraPDF exits cleanly, the job is
// treated as done; if it errors, the job is failed. Deeper spooler inspection
// (jam / offline / out-of-paper) is deferred to a later version.
package printer

import (
	"fmt"
	"os/exec"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// Printer silent-prints PDFs.
type Printer struct {
	sumatraPath string // path to SumatraPDF.exe
}

// New builds a Printer given the path to SumatraPDF.exe.
func New(sumatraPath string) *Printer {
	return &Printer{sumatraPath: sumatraPath}
}

// Print silently prints pdfPath to printerName.
// Returns StateDone if SumatraPDF completes cleanly, StateFailed otherwise.
func (p *Printer) Print(pdfPath, printerName string) (protocol.JobState, error) {
	cmd := exec.Command(
		p.sumatraPath,
		"-print-to", printerName,
		"-silent",
		pdfPath,
	)
	if err := cmd.Run(); err != nil {
		return protocol.StateFailed, fmt.Errorf("sumatra print failed: %w", err)
	}
	return protocol.StateDone, nil
}
