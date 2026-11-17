package main

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tencent-connect/botgo"
	"github.com/tencent-connect/botgo/errs"
	botlog "github.com/tencent-connect/botgo/log"
	"github.com/tencent-connect/botgo/openapi"
	"golang.org/x/oauth2"
)

func TestQQSDKTextImagesAndErrorDetails(t *testing.T) {
	botlog.DefaultLogger = quietSDKLogger{}
	var images []string
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/groups/group/messages" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "QQBot offline-token" {
			t.Error("missing SDK authorization")
		}
		var message struct {
			Content  string `json:"content"`
			Type     int    `json:"msg_type"`
			Markdown *struct {
				Content string `json:"content"`
			} `json:"markdown"`
		}
		if err := json.UnmarshalRead(r.Body, &message); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if message.Content == "rejected" {
			w.Header().Set("X-Tps-trace-ID", "offline-trace")
			w.WriteHeader(400)
			if _, err := w.Write([]byte(`{"code":304023,"message":"proactive message rejected"}`)); err != nil {
				t.Error(err)
			}
			return
		}
		if message.Type == 2 && message.Markdown != nil {
			images = append(images, message.Markdown.Content)
		} else if message.Type == 0 {
			texts = append(texts, message.Content)
		} else {
			t.Errorf("unexpected SDK message: %+v", message)
		}
		if _, err := w.Write([]byte(`{"id":"offline-message"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	destination, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the actual SDK HTTP stack, redirecting only this test's app to localhost.
	openapi.RegisterReqFilter(t.Name(), func(req *http.Request, _ *http.Response) error {
		if req.Header.Get("X-Union-Appid") == "offline-test-app" {
			req.URL.Scheme = destination.Scheme
			req.URL.Host = destination.Host
			req.Host = destination.Host
		}
		return nil
	})
	api := botgo.NewOpenAPI("offline-test-app", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "offline-token", TokenType: "QQBot"}))
	bot := &qqClient{api: api, groupID: "group"}
	if err := bot.send(t.Context(), tellRecord{Msg: "hello", Imgs: []string{"https://example.com/first.png", "https://example.com/second.png?version=2"}}); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 || texts[0] != "hello" {
		t.Errorf("text delivery = %v, want [hello]", texts)
	}
	if len(images) != 2 || images[0] != "![播报图片](https://example.com/first.png)" || images[1] != "![播报图片](https://example.com/second.png?version=2)" {
		t.Errorf("Markdown image delivery = %v", images)
	}
	err = bot.send(t.Context(), tellRecord{Msg: "rejected"})
	var sdkErr *errs.Err
	if !errors.As(err, &sdkErr) {
		t.Fatalf("send rejected record = %v, want wrapped SDK error", err)
	}
	if sdkErr.Code() != 400 || !strings.Contains(sdkErr.Text(), "304023") || !strings.Contains(sdkErr.Text(), "proactive message rejected") || sdkErr.Trace() != "offline-trace" {
		t.Errorf("missing SDK rejection details: %v", err)
	}
	if !strings.Contains(err.Error(), "send QQ text") {
		t.Errorf("missing failing stage: %v", err)
	}
}
