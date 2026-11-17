package main

import (
	"crypto/md5"
	"encoding/json/v2"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
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
	var lifecycle []string
	materialCount := 0
	rejectImage := false
	rejectDelete := false
	var uploadMD5 string
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first.png" || r.URL.Path == "/second.png" {
			w.Header().Set("Content-Type", "image/png")
			if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
				t.Error(err)
			}
			return
		}
		switch {
		case r.URL.Path == "/v2/media-images/prepare":
			lifecycle = append(lifecycle, "prepare")
			var body struct {
				MD5  string `json:"md5"`
				Size int    `json:"file_size"`
				MIME string `json:"content_type"`
				SHA1 string `json:"sha1"`
			}
			if err := json.UnmarshalRead(r.Body, &body); err != nil {
				t.Error(err)
			}
			if body.Size <= 0 || body.MIME != "image/png" || len(body.SHA1) != 40 {
				t.Errorf("invalid prepare payload: %+v", body)
			}
			uploadMD5 = body.MD5
			w.Header().Set("Content-Type", "application/json")
			if err := json.MarshalWrite(w, map[string]any{"upload_id": "upload", "presigned_url": server.URL + "/upload"}); err != nil {
				t.Error(err)
			}
			return
		case r.URL.Path == "/upload":
			lifecycle = append(lifecycle, "put")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if r.Method != http.MethodPut || r.Header.Get("Authorization") != "" || r.Header.Get("Content-Type") != "image/png" || fmt.Sprintf("%x", md5.Sum(data)) != uploadMD5 {
				t.Error("invalid presigned upload")
			}
			w.Header().Set("ETag", "\""+uploadMD5+"\"")
			return
		case r.URL.Path == "/v2/media-images/complete":
			lifecycle = append(lifecycle, "complete")
			materialCount++
			w.Header().Set("Content-Type", "application/json")
			if err := json.MarshalWrite(w, map[string]string{"image_id": fmt.Sprint(materialCount), "url": fmt.Sprintf("qqbot-media://image/v1/token%d", materialCount)}); err != nil {
				t.Error(err)
			}
			return
		case strings.HasPrefix(r.URL.Path, "/v2/media-images/") && strings.HasSuffix(r.URL.Path, "/delete"):
			lifecycle = append(lifecycle, "delete")
			if rejectDelete {
				w.WriteHeader(503)
				return
			}
			if r.Method != http.MethodDelete {
				t.Error("material cleanup must use DELETE")
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{}`)); err != nil {
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
		if rejectImage && message.Type == 2 {
			w.WriteHeader(400)
			if _, err := w.Write([]byte(`{"code":400,"message":"image rejected"}`)); err != nil {
				t.Error(err)
			}
			return
		}
		if message.Content == "rejected" {
			w.Header().Set("X-Tps-trace-ID", "offline-trace")
			w.WriteHeader(400)
			if _, err := w.Write([]byte(`{"code":304023,"message":"proactive message rejected"}`)); err != nil {
				t.Error(err)
			}
			return
		}
		lifecycle = append(lifecycle, "send")
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
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = server.Client().Transport
	plainServer := httptest.NewServer(proxy)
	defer plainServer.Close()
	destination, err := url.Parse(plainServer.URL)
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
	want := "hello\n\n![播报图片 #32px #24px](qqbot-media://image/v1/token1)\n\n![播报图片 #32px #24px](qqbot-media://image/v1/token2)"
	if images[0] != want {
		t.Errorf("combined content = %q, want %q", images[0], want)
	}
	if strings.Join(lifecycle, ",") != "prepare,put,complete,prepare,put,complete,send,delete,delete" {
		t.Errorf("material lifecycle = %v", lifecycle)
	}
	if err := bot.send(t.Context(), tellRecord{Imgs: []string{firstURL}}); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 0 || len(images) != 2 || images[1] != "![播报图片 #32px #24px](qqbot-media://image/v1/token3)" {
		t.Errorf("image-only delivery: texts = %v, Markdown messages = %v; want one additional image-only message", texts, images)
	}
	rejectImage = true
	if err := bot.send(t.Context(), tellRecord{Imgs: []string{firstURL}}); err == nil {
		t.Error("expected image send rejection")
	}
	if len(bot.pendingDeletes) != 0 || lifecycle[len(lifecycle)-1] != "delete" {
		t.Error("rejected image send leaked material")
	}
	rejectImage = false
	rejectDelete = true
	if err := bot.send(t.Context(), tellRecord{Imgs: []string{firstURL}}); err != nil {
		t.Fatalf("accepted send must not fail because cleanup failed: %v", err)
	}
	if len(bot.pendingDeletes) != 1 {
		t.Error("failed deletion was not retained")
	}
	rejectDelete = false
	if err := bot.cleanupMaterials(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(bot.pendingDeletes) != 0 {
		t.Error("cleanup retry left pending material")
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
