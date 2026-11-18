package main

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const hywWebSocketURL = "wss://bot.heyinwu.top/"

const (
	napcatHeartbeat = 10 * time.Second
	napcatReconnect = 5 * time.Second
)

type napcatBridge struct {
	token       string
	selfID      int64
	groupID     int64
	imagePolicy cqImagePolicy
	send        func(context.Context, tellRecord) error
	messageID   int32
}

type napcatResponse struct {
	Status  string         `json:"status"`
	Retcode int            `json:"retcode"`
	Data    any            `json:"data"`
	Echo    jsontext.Value `json:"echo"`
	Message string         `json:"message"`
	Wording string         `json:"wording"`
	Stream  string         `json:"stream"`
}

func (b *napcatBridge) run(ctx context.Context) {
	for ctx.Err() == nil {
		err := b.connect(ctx, hywWebSocketURL, napcatHeartbeat)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("HYW WebSocket disconnected; reconnecting", "error", err)
		timer := time.NewTimer(napcatReconnect)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (b *napcatBridge) connect(ctx context.Context, endpoint string, heartbeat time.Duration) error {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, resp, err := dialer.DialContext(ctx, endpoint, http.Header{
		"Authorization": {"Bearer " + b.token},
		"X-Self-Id":     {strconv.FormatInt(b.selfID, 10)},
		"X-Client-Role": {"Universal"},
		"User-Agent":    {"OneBot/11"},
	})
	if err != nil {
		if resp != nil {
			resp.Body.Close()
			return fmt.Errorf("WebSocket upgrade returned HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("dial HYW WebSocket: %w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	write := func(frame any) error {
		payload, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, payload)
	}
	event := func(kind string, fields map[string]any) error {
		fields["time"] = time.Now().Unix()
		fields["self_id"] = b.selfID
		fields["post_type"] = "meta_event"
		fields["meta_event_type"] = kind
		return write(fields)
	}
	if err := event("lifecycle", map[string]any{"sub_type": "connect"}); err != nil {
		return fmt.Errorf("send connect event: %w", err)
	}
	slog.Info("HYW WebSocket connected")

	// Keep reading control frames and writing heartbeats while QQ uploads are in flight.
	ctx, cancel := context.WithCancelCause(ctx)
	var workers sync.WaitGroup
	requests := make(chan []byte, 16)
	responses := make(chan napcatResponse)
	defer func() {
		cancel(nil)
		conn.Close()
		workers.Wait()
	}()
	workers.Go(func() {
		for {
			kind, frame, err := conn.ReadMessage()
			if err != nil {
				if closeErr, ok := err.(*websocket.CloseError); ok {
					err = fmt.Errorf("WebSocket close code %d", closeErr.Code)
				}
				cancel(err)
				return
			}
			if kind != websocket.TextMessage {
				continue
			}
			select {
			case requests <- frame:
			default:
				cancel(fmt.Errorf("WebSocket request queue full"))
				return
			}
		}
	})
	workers.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-requests:
				if ctx.Err() != nil {
					return
				}
				reply := b.handle(ctx, frame)
				select {
				case responses <- reply:
				case <-ctx.Done():
					return
				}
			}
		}
	})
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
			if err := event("heartbeat", map[string]any{"interval": heartbeat.Milliseconds(), "status": map[string]bool{"online": true, "good": true}}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		case reply := <-responses:
			if err := write(reply); err != nil {
				return fmt.Errorf("send action reply: %w", err)
			}
		}
	}
}

func (b *napcatBridge) handle(ctx context.Context, frame []byte) napcatResponse {
	var request struct {
		Action string         `json:"action"`
		Params jsontext.Value `json:"params"`
		Echo   jsontext.Value `json:"echo"`
	}
	reply := napcatResponse{Status: "ok", Stream: "normal-action", Echo: jsontext.Value(`""`)}
	fail := func(code int, message string) napcatResponse {
		reply.Status, reply.Retcode, reply.Message, reply.Wording = "failed", code, message, message
		return reply
	}
	if err := json.Unmarshal(frame, &request); err != nil || request.Action == "" {
		return fail(1400, "invalid action request")
	}
	if len(request.Echo) != 0 {
		reply.Echo = request.Echo
	}
	switch request.Action {
	case "get_login_info":
		reply.Data = map[string]any{"user_id": b.selfID, "nickname": "HYW Reporter"}
	case "send_group_msg":
		var params struct {
			GroupID    jsontext.Value `json:"group_id"`
			Message    jsontext.Value `json:"message"`
			AutoEscape bool           `json:"auto_escape"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.Message.Kind() != '"' {
			return fail(1400, "expected group_id and string message")
		}
		group := string(params.GroupID)
		if params.GroupID.Kind() == '"' && json.Unmarshal(params.GroupID, &group) != nil {
			return fail(1400, "invalid group_id")
		}
		id, err := strconv.ParseInt(group, 10, 64)
		if err != nil {
			return fail(1400, "invalid group_id")
		}
		if id != b.groupID {
			slog.Warn("ignoring HYW broadcast for unconfigured group", "incoming_group_id", id, "configured_group_id", b.groupID)
			return fail(1403, "destination group not configured")
		}
		var message string
		if json.Unmarshal(params.Message, &message) != nil {
			return fail(1400, "invalid message")
		}
		record, err := parseCQBroadcast(message, params.AutoEscape, b.imagePolicy)
		if err != nil {
			slog.Warn("rejecting HYW broadcast payload", "error", err)
			return fail(1400, err.Error())
		}
		if err := b.send(ctx, record); err != nil {
			slog.Warn("WebSocket broadcast delivery failed", "error", err)
			return fail(1200, "official QQ delivery failed")
		}
		// Official QQ IDs are opaque strings; OneBot requires an int32 message ID.
		if b.messageID == math.MaxInt32 {
			b.messageID = 0
		}
		b.messageID++
		reply.Data = map[string]int32{"message_id": b.messageID}
	default:
		return fail(1404, "unsupported action")
	}
	return reply
}
