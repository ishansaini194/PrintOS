// Package updater keeps the agent binary up to date: it asks the cloud for the
// latest version, downloads it if newer, verifies the checksum, swaps the
// running binary, and signals a restart. Building this early means future bugs
// become a remote push instead of a shop visit.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// LatestInfo is what the cloud returns when asked for the latest agent build.
type LatestInfo struct {
	Version string `json:"version"`
	URL     string `json:"url"`    // where to download the new binary
	SHA256  string `json:"sha256"` // expected checksum
}

// Updater checks for and applies new agent builds.
type Updater struct {
	checkURL       string        // cloud endpoint returning LatestInfo
	currentVersion string        // this build's version
	interval       time.Duration // how often to check
	client         *http.Client
}

// New builds an Updater.
func New(checkURL, currentVersion string, interval time.Duration) *Updater {
	return &Updater{
		checkURL:       checkURL,
		currentVersion: currentVersion,
		interval:       interval,
		client:         &http.Client{Timeout: 30 * time.Second},
	}
}

// Run checks periodically until stop is closed. When a newer version is applied
// it calls onUpdated (e.g. to trigger a restart) and returns.
func (u *Updater) Run(stop <-chan struct{}, onUpdated func()) {
	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			applied, err := u.checkAndApply()
			if err != nil {
				continue // try again next tick
			}
			if applied {
				onUpdated()
				return
			}
		}
	}
}

// checkAndApply fetches latest info, and if newer, downloads + swaps the binary.
// Returns true if an update was applied.
func (u *Updater) checkAndApply() (bool, error) {
	info, err := u.fetchLatest()
	if err != nil {
		return false, err
	}
	if info.Version == u.currentVersion || info.Version == "" {
		return false, nil // already current
	}

	data, err := u.download(info.URL)
	if err != nil {
		return false, err
	}
	if !checksumOK(data, info.SHA256) {
		return false, fmt.Errorf("checksum mismatch — refusing to install")
	}
	if err := swapBinary(data); err != nil {
		return false, err
	}
	return true, nil
}

// fetchLatest asks the cloud what the newest version is.
func (u *Updater) fetchLatest() (LatestInfo, error) {
	var info LatestInfo
	resp, err := u.client.Get(u.checkURL)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("check status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return info, err
	}
	return info, nil
}

// download fetches the new binary bytes.
func (u *Updater) download(url string) ([]byte, error) {
	resp, err := u.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// checksumOK verifies the downloaded bytes match the expected SHA-256.
func checksumOK(data []byte, expected string) bool {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) == expected
}

// swapBinary replaces the currently running executable with the new bytes.
// It writes the new binary next to the current one, then renames it into place.
func swapBinary(data []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)

	tmp, err := os.CreateTemp(dir, "agent-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	// Rename the new file over the old one. On Windows a running exe can't be
	// overwritten directly, so we move the old aside first.
	old := exe + ".old"
	os.Remove(old) // clean any leftover
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		os.Rename(old, exe) // roll back
		return err
	}
	return nil
}
