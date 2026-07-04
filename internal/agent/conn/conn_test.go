package conn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

var upgrader = websocket.Upgrader{}

// wsURL converts an httptest http:// URL to ws://.
func wsURL(s string) string {
	return "ws" + strings.TrimPrefix(s, "http")
}

func TestReceiveDispatchesToHandler(t *testing.T) {
	// server sends one envelope, then holds
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		env := protocol.Envelope{Type: protocol.MsgJobPush, ProtocolVersion: protocol.Version}
		data, _ := json.Marshal(env)
		ws.WriteMessage(websocket.TextMessage, data)
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	got := make(chan protocol.Envelope, 1)
	c := New(wsURL(srv.URL), func(e protocol.Envelope) error {
		got <- e
		return nil
	})

	stop := make(chan struct{})
	go c.Run(stop)
	defer close(stop)

	select {
	case e := <-got:
		if e.Type != protocol.MsgJobPush {
			t.Errorf("expected job_push, got %s", e.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never called")
	}
}

func TestSend(t *testing.T) {
	received := make(chan protocol.Envelope, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var e protocol.Envelope
		json.Unmarshal(data, &e)
		received <- e
	}))
	defer srv.Close()

	c := New(wsURL(srv.URL), func(protocol.Envelope) error { return nil })
	stop := make(chan struct{})
	go c.Run(stop)
	defer close(stop)

	// wait for connection to establish
	time.Sleep(200 * time.Millisecond)
	err := c.Send(protocol.Envelope{Type: protocol.MsgHeartbeat, ProtocolVersion: protocol.Version})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case e := <-received:
		if e.Type != protocol.MsgHeartbeat {
			t.Errorf("expected heartbeat, got %s", e.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received message")
	}
}

func TestSendNotConnected(t *testing.T) {
	c := New("ws://127.0.0.1:0", func(protocol.Envelope) error { return nil })
	if err := c.Send(protocol.Envelope{Type: protocol.MsgHeartbeat}); err == nil {
		t.Error("expected error when not connected")
	}
}

func TestReconnectsAfterDrop(t *testing.T) {
	var conns int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns++
		ws.Close() // drop immediately to force reconnect
	}))
	defer srv.Close()

	c := New(wsURL(srv.URL), func(protocol.Envelope) error { return nil })
	stop := make(chan struct{})
	go c.Run(stop)

	// backoff starts at 1s; allow two dial attempts
	time.Sleep(1500 * time.Millisecond)
	close(stop)

	if conns < 2 {
		t.Errorf("expected at least 2 connection attempts, got %d", conns)
	}
}
