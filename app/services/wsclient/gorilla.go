package wsclient

import (
	"context"
	"net/http"
	"time"

	"hostlink/internal/wsprotocol"

	"github.com/gorilla/websocket"
)

type DefaultDialer struct{}

func (DefaultDialer) Dial(ctx context.Context, url string, headers http.Header) (Conn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, headers)
	if err != nil {
		return nil, err
	}
	return &gorillaConn{conn: conn}, nil
}

type gorillaConn struct {
	conn *websocket.Conn
}

func (c *gorillaConn) WriteEnvelope(ctx context.Context, env wsprotocol.Envelope) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(deadline)
	}
	return c.conn.WriteJSON(env)
}

func (c *gorillaConn) ReadEnvelope(ctx context.Context) (wsprotocol.Envelope, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetReadDeadline(deadline)
	}
	var env wsprotocol.Envelope
	err := c.conn.ReadJSON(&env)
	return env, err
}

func (c *gorillaConn) Ping(ctx context.Context) error {
	deadline := time.Now().Add(10 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	return c.conn.WriteControl(websocket.PingMessage, nil, deadline)
}

func (c *gorillaConn) Close() error {
	return c.conn.Close()
}
