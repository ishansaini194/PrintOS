// Package razorpay is a thin client for the two Razorpay API calls the cloud
// makes (create order, refund payment) plus checkout signature verification.
// It deliberately avoids the full SDK: the surface is tiny, and a plain HTTP
// client keeps the base URL injectable for tests.
package razorpay

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL is Razorpay's live API root (test keys use the same host).
const DefaultBaseURL = "https://api.razorpay.com/v1"

// ErrNotConfigured is returned when the key id/secret are missing — payments
// cannot work until RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET are set.
var ErrNotConfigured = errors.New("razorpay keys not configured")

// Client talks to the Razorpay REST API with key-id/secret basic auth.
type Client struct {
	keyID     string
	keySecret string
	baseURL   string
	hc        *http.Client
}

// New builds a client. baseURL "" means the real Razorpay API.
func New(keyID, keySecret, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		keyID:     keyID,
		keySecret: keySecret,
		baseURL:   strings.TrimRight(baseURL, "/"),
		hc:        &http.Client{Timeout: 15 * time.Second},
	}
}

// NewFromEnv builds a client from RAZORPAY_KEY_ID / RAZORPAY_KEY_SECRET
// (RAZORPAY_BASE_URL overrides the API root, for tests). Missing keys are not
// fatal here — calls will return ErrNotConfigured until they are set.
func NewFromEnv() *Client {
	return New(
		os.Getenv("RAZORPAY_KEY_ID"),
		os.Getenv("RAZORPAY_KEY_SECRET"),
		os.Getenv("RAZORPAY_BASE_URL"),
	)
}

// KeyID is the public key id, safe to hand to the browser checkout.
func (c *Client) KeyID() string { return c.keyID }

// CreateOrder creates an INR order for the amount and returns Razorpay's
// order id. receipt is our job id, for reconciliation in the dashboard.
func (c *Client) CreateOrder(amountPaise int, receipt string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := c.post("/orders", map[string]any{
		"amount":   amountPaise,
		"currency": "INR",
		"receipt":  receipt,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("razorpay order response missing id")
	}
	return out.ID, nil
}

// RefundPayment refunds the captured payment in full and returns Razorpay's
// refund id and status ("processed"/"pending").
func (c *Client) RefundPayment(razorpayPaymentID string, amountPaise int) (refundID, status string, err error) {
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	err = c.post("/payments/"+razorpayPaymentID+"/refund", map[string]any{
		"amount": amountPaise,
	}, &out)
	if err != nil {
		return "", "", err
	}
	if out.ID == "" {
		return "", "", errors.New("razorpay refund response missing id")
	}
	return out.ID, out.Status, nil
}

// VerifySignature checks the checkout callback signature: HMAC-SHA256 of
// "order_id|payment_id" with the key secret, hex-encoded, compared in
// constant time. This is the gate that stops a browser from lying about
// having paid.
func (c *Client) VerifySignature(orderID, paymentID, signature string) bool {
	if c.keySecret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(c.keySecret))
	mac.Write([]byte(orderID + "|" + paymentID))
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(want), []byte(signature)) == 1
}

// post sends an authenticated JSON POST and decodes the JSON response into out.
func (c *Client) post(path string, body map[string]any, out any) error {
	if c.keyID == "" || c.keySecret == "" {
		return ErrNotConfigured
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("razorpay %s: HTTP %d: %s", path, resp.StatusCode, truncate(raw, 300))
	}
	return json.Unmarshal(raw, out)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
