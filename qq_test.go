package main

import (
	"encoding/json/v2"
	"errors"
	"image"
	"image/png"
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
		if r.URL.Path == "/first.png" || r.URL.Path == "/second.png" {
			w.Header().Set("Content-Type", "image/png")
			if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
				t.Error(err)
			}
			return
		}
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
	bot := &qqClient{api: api, groupID: "group", imageClient: server.Client()}
	firstURL, secondURL := server.URL+"/first.png", server.URL+"/second.png?version=2"
	if err := bot.send(t.Context(), tellRecord{Msg: "hello", Imgs: []string{firstURL, secondURL}}); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 0 || len(images) != 1 {
		t.Fatalf("combined delivery: texts = %v, Markdown messages = %v; want exactly one Markdown message", texts, images)
	}
	want := "hello\n\n![播报图片 #32px #24px](" + firstURL + ")\n\n![播报图片 #32px #24px](" + secondURL + ")"
	if images[0] != want {
		t.Errorf("combined content = %q, want %q", images[0], want)
	}
	if err := bot.send(t.Context(), tellRecord{Imgs: []string{firstURL}}); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 0 || len(images) != 2 || images[1] != "![播报图片 #32px #24px]("+firstURL+")" {
		t.Errorf("image-only delivery: texts = %v, Markdown messages = %v; want one additional image-only message", texts, images)
	}
	if err := bot.send(t.Context(), tellRecord{Msg: "plain text"}); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 || texts[0] != "plain text" {
		t.Errorf("text-only delivery = %v, want [plain text]", texts)
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
