package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPollBaselineBacklogAndFailure(t *testing.T) {
	records := []tellRecord{{RID: "old", Time: 1}}
	detailCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tell_log_list":
			var request struct {
				Page int `json:"page"`
			}
			if err := json.UnmarshalRead(r.Body, &request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			start := min((request.Page-1)*50, len(records))
			end := min(start+50, len(records))
			if err := json.MarshalWrite(w, map[string]any{"code": 200, "data": map[string]any{"list": records[start:end], "total": len(records)}}); err != nil {
				t.Error(err)
			}
		case "/tell_log_detail":
			detailCalls++
			var request struct {
				RID string `json:"rid"`
			}
			if err := json.UnmarshalRead(r.Body, &request); err != nil {
				t.Error(err)
				return
			}
			if err := json.MarshalWrite(w, map[string]any{"code": 200, "data": tellRecord{RID: request.RID, Msg: request.RID}}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected admin write endpoint %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	source := &heyinwuClient{baseURL: server.URL, token: "session", httpClient: server.Client()}
	state := filepath.Join(t.TempDir(), "state.json")
	var delivered []string
	fail := false
	send := func(ctx context.Context, record tellRecord) error {
		if fail {
			return errors.New("delivery rejected")
		}
		delivered = append(delivered, record.RID)
		return nil
	}
	if err := runPoll(t.Context(), source, 45, "group", state, send); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 0 || detailCalls != 0 {
		t.Errorf("baseline delivered %v, detail calls %d", delivered, detailCalls)
	}
	for i := 1; i <= 55; i++ {
		records = append([]tellRecord{{RID: fmt.Sprint(i), Time: int64(i + 1)}}, records...)
	}
	fail = true
	if err := runPoll(t.Context(), source, 45, "group", state, send); err == nil {
		t.Fatal("expected rejected delivery")
	}
	fail = false
	if err := runPoll(t.Context(), source, 45, "group", state, send); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 55 || delivered[0] != "1" || delivered[54] != "55" {
		t.Errorf("delivery order/count = %v", delivered)
	}
	if err := runPoll(t.Context(), source, 45, "group", state, send); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 55 {
		t.Errorf("repeated poll delivered %d records, want 55", len(delivered))
	}
	if err := runPoll(t.Context(), source, 45, "different", state, send); err == nil {
		t.Error("expected group mismatch error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestQQTextAndImages(t *testing.T) {
	var calls []string
	var imageContents []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		var payload map[string]any
		if err := json.UnmarshalRead(r.Body, &payload); err != nil {
			t.Error(err)
		}
		response := `{"id":"message"}`
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			response = `{"access_token":"secret-token","expires_in":"7200"}`
		case "/v2/groups/group/messages":
			if r.Header.Get("Authorization") != "QQBot secret-token" {
				t.Error("missing QQ authorization")
			}
			if payload["msg_type"] == float64(2) {
				markdown, ok := payload["markdown"].(map[string]any)
				if !ok {
					t.Error("image message has no Markdown body")
				} else {
					content, _ := markdown["content"].(string)
					imageContents = append(imageContents, content)
				}
				if _, ok := payload["media"]; ok {
					t.Error("Markdown image must not use uploaded media")
				}
			} else if payload["msg_type"] != float64(0) || payload["content"] != "hello" {
				t.Errorf("unexpected text payload: %v", payload)
			}
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	bot := &qqClient{appID: "app", secret: "secret", groupID: "group", httpClient: client}
	if err := bot.send(t.Context(), tellRecord{Msg: "hello", Imgs: []string{"https://example.com/first.png", "https://example.com/second.png?version=2"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/app/getAppAccessToken", "/v2/groups/group/messages", "/v2/groups/group/messages", "/v2/groups/group/messages"}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("QQ request flow = %v, want %v", calls, want)
	}
	wantImages := []string{"![播报图片](https://example.com/first.png)", "![播报图片](https://example.com/second.png?version=2)"}
	if fmt.Sprint(imageContents) != fmt.Sprint(wantImages) {
		t.Errorf("Markdown images = %v, want %v", imageContents, wantImages)
	}
}
