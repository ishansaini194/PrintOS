package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	c := New("key", "secret", "")

	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("order_1|pay_1"))
	valid := hex.EncodeToString(mac.Sum(nil))

	if !c.VerifySignature("order_1", "pay_1", valid) {
		t.Error("valid signature rejected")
	}
	if c.VerifySignature("order_1", "pay_1", valid[:len(valid)-1]+"0") {
		t.Error("tampered signature accepted")
	}
	if c.VerifySignature("order_2", "pay_1", valid) {
		t.Error("signature accepted for a different order")
	}
	if (&Client{}).VerifySignature("order_1", "pay_1", valid) {
		t.Error("unconfigured client verified a signature")
	}
}

func TestCreateOrderAndRefund(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "key" || pass != "secret" {
			t.Errorf("basic auth = %q/%q, want key/secret", user, pass)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/orders":
			if body["amount"].(float64) != 500 || body["currency"] != "INR" || body["receipt"] != "job-1" {
				t.Errorf("order body = %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "order_test1"})
		case "/payments/pay_9/refund":
			if body["amount"].(float64) != 500 {
				t.Errorf("refund body = %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "rfnd_1", "status": "processed"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := New("key", "secret", srv.URL)

	orderID, err := c.CreateOrder(500, "job-1")
	if err != nil || orderID != "order_test1" {
		t.Fatalf("CreateOrder = %q, %v", orderID, err)
	}
	refundID, status, err := c.RefundPayment("pay_9", 500)
	if err != nil || refundID != "rfnd_1" || status != "processed" {
		t.Fatalf("RefundPayment = %q, %q, %v", refundID, status, err)
	}
}

func TestErrorPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"description":"bad amount"}}`))
	}))
	defer srv.Close()

	if _, err := New("key", "secret", srv.URL).CreateOrder(0, "j"); err == nil {
		t.Error("HTTP 400 did not surface as an error")
	}
	if _, err := New("", "", srv.URL).CreateOrder(100, "j"); err != ErrNotConfigured {
		t.Errorf("missing keys: err = %v, want ErrNotConfigured", err)
	}
}
