// Package download fetches a job's PDF to a local temp file before printing,
// verifying its checksum so a corrupted or wrong file never reaches the printer.
package download

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// client is shared; a generous timeout covers slow shop links.
var client = &http.Client{Timeout: 30 * time.Second}

// ToTempFile downloads url into a temp .pdf file. If expectedSHA is non-empty
// the bytes are verified against it (mismatch → error, temp file removed).
// On success it returns the temp path and a cleanup func the caller must defer.
func ToTempFile(url, expectedSHA string) (path string, cleanup func(), err error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}

	if expectedSHA != "" && !checksumOK(data, expectedSHA) {
		return "", nil, fmt.Errorf("pdf checksum mismatch")
	}

	f, err := os.CreateTemp("", "printos-*.pdf")
	if err != nil {
		return "", nil, err
	}
	tmpPath := f.Name()
	remove := func() { os.Remove(tmpPath) }

	if _, err := f.Write(data); err != nil {
		f.Close()
		remove()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		remove()
		return "", nil, err
	}

	return tmpPath, remove, nil
}

// checksumOK verifies the bytes match the expected SHA-256 (hex).
func checksumOK(data []byte, expected string) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == expected
}
