package main

import (
	"context"
	"fmt"
	"image"
	"io"
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
	api         openapi.OpenAPI
	groupID     string
	imageClient *http.Client
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
	var content strings.Builder
	content.WriteString(record.Msg)
	hasImages := false
	for i, imageURL := range record.Imgs {
		parsed, err := url.Parse(imageURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			continue
		}
		width, height, err := c.imageSize(ctx, imageURL)
		if err != nil {
			return fmt.Errorf("inspect QQ image %d: %w", i+1, err)
		}
		if content.Len() > 0 {
			content.WriteString("\n\n")
		}
		fmt.Fprintf(&content, "![播报图片 #%dpx #%dpx](%s)", width, height, imageURL)
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

func (c *qqClient) imageSize(ctx context.Context, imageURL string) (int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return 0, 0, err
	}
	res, err := c.imageClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch image header: %w", err)
	}
	defer res.Body.Close() // Only image metadata is read; close errors cannot change it.
	if res.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("image HTTP status %d", res.StatusCode)
	}
	config, _, err := image.DecodeConfig(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return 0, 0, fmt.Errorf("decode image dimensions: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return 0, 0, fmt.Errorf("invalid image dimensions")
	}
	return config.Width, config.Height, nil
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
