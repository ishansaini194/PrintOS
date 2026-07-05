// Package auth provisions the agent on first run: it exchanges a one-time
// setup code for a long-lived token, saves the token locally, and reuses it on
// every subsequent start.
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// identity is what the agent persists locally and sends to the cloud.
type identity struct {
	ShopID string `json:"shop_id"`
	Token  string `json:"token"`
}

// EnsureToken returns this agent's shop id and token, provisioning if needed.
//
//   - If tokenPath exists, its saved {shop_id, token} is read and returned.
//   - Otherwise, if setupCode is set, it is POSTed to provisionURL, and the
//     returned {shop_id, token} is saved to tokenPath (0600) and returned.
//   - If there is no saved token and no setup code, it returns an error.
func EnsureToken(provisionURL, setupCode, tokenPath string) (shopID, token string, err error) {
	data, err := os.ReadFile(tokenPath)
	if err == nil {
		var id identity
		if err := json.Unmarshal(data, &id); err != nil {
			return "", "", fmt.Errorf("read token file %s: %w", tokenPath, err)
		}
		if id.ShopID == "" || id.Token == "" {
			return "", "", fmt.Errorf("token file %s is incomplete", tokenPath)
		}
		return id.ShopID, id.Token, nil
	}
	if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("read token file %s: %w", tokenPath, err)
	}

	// Not provisioned yet — a setup code is required to bootstrap.
	if setupCode == "" {
		return "", "", fmt.Errorf("not provisioned: set PRINTOS_SETUP_CODE for first run")
	}

	id, err := provision(provisionURL, setupCode)
	if err != nil {
		return "", "", err
	}
	saved, _ := json.Marshal(id)
	if err := os.WriteFile(tokenPath, saved, 0o600); err != nil {
		return "", "", fmt.Errorf("save token file %s: %w", tokenPath, err)
	}
	return id.ShopID, id.Token, nil
}

// provision exchanges the setup code for an identity with the cloud.
func provision(provisionURL, setupCode string) (identity, error) {
	body, _ := json.Marshal(map[string]string{"setup_code": setupCode})
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(provisionURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return identity{}, fmt.Errorf("provision request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return identity{}, fmt.Errorf("provision failed: status %d", resp.StatusCode)
	}

	var id identity
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		return identity{}, fmt.Errorf("decode provision response: %w", err)
	}
	if id.ShopID == "" || id.Token == "" {
		return identity{}, fmt.Errorf("provision returned an empty identity")
	}
	return id, nil
}
