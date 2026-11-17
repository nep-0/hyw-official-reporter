package main

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const qqAPIBase = "https://api.sgroup.qq.com"
const qqTokenURL = "https://bots.qq.com/app/getAppAccessToken"

type qqClient struct {
	appID       string
	secret      string
	groupID     string
	token       string
	tokenExpiry time.Time
	httpClient  *http.Client
}

func (c *qqClient) accessToken(ctx context.Context) error {
	if c.token != "" && time.Until(c.tokenExpiry) > time.Minute {
		return nil
	}
	var response struct {
		Code    int    `json:"code"`
		Token   string `json:"access_token"`
		Expires string `json:"expires_in"`
	}
	if err := postJSON(ctx, c.httpClient, qqTokenURL, map[string]string{"appId": c.appID, "clientSecret": c.secret}, "", "", &response); err != nil {
		return fmt.Errorf("request QQ access token: %w", err)
	}
	if response.Code != 0 || response.Token == "" {
		return fmt.Errorf("QQ token endpoint returned code %d", response.Code)
	}
	seconds, err := strconv.Atoi(response.Expires)
	if err != nil || seconds <= 0 {
		return fmt.Errorf("QQ token endpoint returned invalid expiry")
	}
	c.token, c.tokenExpiry = response.Token, time.Now().Add(time.Duration(seconds)*time.Second)
	return nil
}

func (c *qqClient) send(ctx context.Context, record tellRecord) error {
	if err := c.accessToken(ctx); err != nil {
		return err
	}
	text := record.Msg
	if text == "" {
		text = "[图片播报]"
	}
	if err := c.sendText(ctx, text); err != nil {
		return err
	}
	for _, imageURL := range record.Imgs {
		parsed, err := url.Parse(imageURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			continue
		}
		if err := c.sendImage(ctx, imageURL); err != nil {
			return err
		}
	}
	return nil
}

func (c *qqClient) sendText(ctx context.Context, text string) error {
	body := map[string]any{"content": text, "msg_type": 0}
	return c.qqPost(ctx, "/v2/groups/"+url.PathEscape(c.groupID)+"/messages", body, nil)
}

func (c *qqClient) sendImage(ctx context.Context, imageURL string) error {
	// Match rmj-card-go's inline Markdown images; QQ fetches the public URL.
	body := map[string]any{
		"msg_type": 2,
		"markdown": map[string]string{"content": fmt.Sprintf("![播报图片](%s)", imageURL)},
	}
	return c.qqPost(ctx, "/v2/groups/"+url.PathEscape(c.groupID)+"/messages", body, nil)
}

func (c *qqClient) qqPost(ctx context.Context, path string, payload any, result any) error {
	var response jsontext.Value
	if err := postJSON(ctx, c.httpClient, qqAPIBase+path, payload, c.token, c.appID, &response); err != nil {
		return fmt.Errorf("QQ API: %w", err)
	}
	var envelope struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return fmt.Errorf("QQ API code %d", envelope.Code)
	}
	if result != nil {
		return json.Unmarshal(response, result)
	}
	return nil
}
