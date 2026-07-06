package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ishansaini194/PrintOS/internal/agent/printer"
	"github.com/ishansaini194/PrintOS/internal/agent/printerinfo"
	"github.com/ishansaini194/PrintOS/internal/agent/queue"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// sleepingSumatra writes a fake SumatraPDF that records "start <printer> <ns>"
// then, after sleeping, "end <printer> <ns>" to logPath. The printer name is
// SumatraPDF's argument after -print-to ($2), so the log also proves each job
// was targeted at the right printer. Returns the script path.
func sleepingSumatra(t *testing.T, logPath string, sleep time.Duration) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sumatra is a POSIX shell script")
	}
	secs := strconv.FormatFloat(sleep.Seconds(), 'f', 3, 64)
	script := "#!/bin/sh\n" +
		"echo \"start $2 $(date +%s%N)\" >> " + logPath + "\n" +
		"sleep " + secs + "\n" +
		"echo \"end $2 $(date +%s%N)\" >> " + logPath + "\n" +
		"exit 0\n"
	path := filepath.Join(t.TempDir(), "sumatra.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestWorkersPrintConcurrently pushes one mono and one color job and runs one
// worker per printer. It verifies both prints overlap in time (true parallel
// printing, not sequential) and that cancelling the context stops every worker.
func TestWorkersPrintConcurrently(t *testing.T) {
	// A tiny HTTP server standing in for the cloud's PDF host.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("%PDF-1.4 fake\n"))
	}))
	defer srv.Close()

	logPath := filepath.Join(t.TempDir(), "prints.log")
	p := printer.New(sleepingSumatra(t, logPath, 300*time.Millisecond))

	q, err := queue.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer q.Close()

	printers := []printerinfo.Printer{
		{Name: "Mono-A", Type: "mono"},
		{Name: "Color-A", Type: "color"},
	}
	a := New(Config{}, q, p, printers)

	// Empty PDFSHA256 skips checksum verification for the fake bytes.
	mono := protocol.Job{ID: "mono1", Type: "mono", IdempotencyKey: "km", PDFURL: srv.URL}
	color := protocol.Job{ID: "color1", Type: "color", IdempotencyKey: "kc", PDFURL: srv.URL}
	if err := q.Enqueue(mono); err != nil {
		t.Fatalf("enqueue mono: %v", err)
	}
	if err := q.Enqueue(color); err != nil {
		t.Fatalf("enqueue color: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, pr := range printers {
		wg.Add(1)
		go func(pr printerinfo.Printer) {
			defer wg.Done()
			a.worker(ctx, pr)
		}(pr)
	}

	// Wait until both jobs leave the queue (printed).
	waitDrained(t, q, 3*time.Second)

	// Cancel and confirm all workers stop — no leaked goroutines past shutdown.
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not stop after context cancel")
	}

	assertOverlappingPrints(t, logPath)
}

// waitDrained polls until no jobs remain queued/printing, or fails after timeout.
func waitDrained(t *testing.T, q *queue.Queue, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pending, err := q.Pending()
		if err != nil {
			t.Fatalf("pending: %v", err)
		}
		if len(pending) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("jobs did not drain from the queue in time")
}

// assertOverlappingPrints parses the fake sumatra log and checks that the two
// printers' print intervals overlapped — i.e. they printed at the same time.
func assertOverlappingPrints(t *testing.T, logPath string) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read print log: %v", err)
	}
	starts := map[string]int64{}
	ends := map[string]int64{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("bad log line %q", line)
		}
		ts, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			t.Fatalf("bad timestamp in %q: %v", line, err)
		}
		switch f[0] {
		case "start":
			starts[f[1]] = ts
		case "end":
			ends[f[1]] = ts
		}
	}

	for _, name := range []string{"Mono-A", "Color-A"} {
		if _, ok := starts[name]; !ok {
			t.Fatalf("printer %s never printed (log: %s)", name, data)
		}
	}

	// Two intervals overlap iff the later start precedes the earlier end.
	laterStart := max64(starts["Mono-A"], starts["Color-A"])
	earlierEnd := min64(ends["Mono-A"], ends["Color-A"])
	if laterStart >= earlierEnd {
		t.Errorf("prints did not overlap (sequential): starts=%v ends=%v", starts, ends)
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
