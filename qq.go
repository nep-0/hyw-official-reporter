package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/tencent-connect/botgo"
	"github.com/tencent-connect/botgo/dto"
	botlog "github.com/tencent-connect/botgo/log"
	"github.com/tencent-connect/botgo/openapi"
	"github.com/tencent-connect/botgo/token"
)

type qqClient struct {
	api     openapi.OpenAPI
	groupID string
}

func newQQClient(appID, secret, groupID string) *qqClient {
	// SDK v0.2.1 logs credentials/tokens at Debug and response bodies at Info.
	// Report errors at our boundary instead, preserving QQ error bodies and trace IDs.
	botlog.DefaultLogger = quietSDKLogger{}
	source := token.NewQQBotTokenSource(&token.QQBotCredentials{AppID: appID, AppSecret: secret})
	return &qqClient{api: botgo.NewOpenAPI(appID, source).WithTimeout(30 * time.Second), groupID: groupID}
}

func (c *qqClient) send(ctx context.Context, record tellRecord) error {
	text := record.Msg
	if text == "" {
		text = "[图片播报]"
	}
	if _, err := c.api.PostGroupMessage(ctx, c.groupID, &dto.MessageToCreate{Content: text}); err != nil {
		return fmt.Errorf("send QQ text: %w", err)
	}
	for i, imageURL := range record.Imgs {
		parsed, err := url.Parse(imageURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			continue
		}
		message := &dto.MessageToCreate{MsgType: dto.MarkdownMsg, Markdown: &dto.Markdown{Content: fmt.Sprintf("![播报图片](%s)", imageURL)}}
		if _, err := c.api.PostGroupMessage(ctx, c.groupID, message); err != nil {
			return fmt.Errorf("send QQ image %d: %w", i+1, err)
		}
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
