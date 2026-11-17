package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tencent-connect/botgo"
	"github.com/tencent-connect/botgo/dto"
	botlog "github.com/tencent-connect/botgo/log"
	"github.com/tencent-connect/botgo/openapi"
	"github.com/tencent-connect/botgo/token"

	_ "image/gif" // Broadcast images can use any of these standard web formats.
	_ "image/jpeg"
	_ "image/png"
)

type qqClient struct {
	api            openapi.OpenAPI
	groupID        string
	imageClient    *http.Client
	pendingDeletes []string
}

func newQQClient(appID, secret, groupID string) *qqClient {
	// SDK v0.2.1 logs credentials/tokens at Debug and response bodies at Info.
	// Report errors at our boundary instead, preserving QQ error bodies and trace IDs.
	botlog.DefaultLogger = quietSDKLogger{}
	source := token.NewQQBotTokenSource(&token.QQBotCredentials{AppID: appID, AppSecret: secret})
	return &qqClient{
		api:         botgo.NewOpenAPI(appID, source).WithTimeout(30 * time.Second),
		groupID:     groupID,
		imageClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *qqClient) send(ctx context.Context, record tellRecord) error {
	if err := c.cleanupMaterials(ctx); err != nil {
		return err
	}
	defer func() {
		// Cleanup must still run if sending was canceled. A cleanup failure must
		// not turn an accepted message into a delivery retry and duplicate it.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := c.cleanupMaterials(cleanupCtx); err != nil {
			slog.Warn("delete QQ materials; retrying before next delivery", "error", err)
		}
	}()
	var content strings.Builder
	content.WriteString(record.Msg)
	hasImages := false
	for i, imageURL := range record.Imgs {
		parsed, err := url.Parse(imageURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			continue
		}
		material, width, height, err := c.uploadImage(ctx, imageURL)
		if err != nil {
			return fmt.Errorf("upload QQ image %d: %w", i+1, err)
		}
		if content.Len() > 0 {
			content.WriteString("\n\n")
		}
		fmt.Fprintf(&content, "![播报图片 #%dpx #%dpx](%s)", width, height, material)
		hasImages = true
	}
	message := &dto.MessageToCreate{Content: record.Msg}
	stage := "text"
	if hasImages {
		message = &dto.MessageToCreate{MsgType: dto.MarkdownMsg, Markdown: &dto.Markdown{Content: content.String()}}
		stage = "text and images"
	}
	if _, err := c.api.PostGroupMessage(ctx, c.groupID, message); err != nil {
		return fmt.Errorf("send QQ %s: %w", stage, err)
	}
	return nil
}

// quietSDKLogger prevents SDK credential and response-body logs. Returned SDK
// errors are logged once by the poll loop, with the operation and record ID.
type quietSDKLogger struct{}

func (quietSDKLogger) Debug(...any)          {}
func (quietSDKLogger) Info(...any)           {}
func (quietSDKLogger) Warn(...any)           {}
func (quietSDKLogger) Error(...any)          {}
func (quietSDKLogger) Debugf(string, ...any) {}
func (quietSDKLogger) Infof(string, ...any)  {}
func (quietSDKLogger) Warnf(string, ...any)  {}
func (quietSDKLogger) Errorf(string, ...any) {}
func (quietSDKLogger) Sync() error           { return nil }
