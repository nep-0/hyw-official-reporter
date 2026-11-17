package main

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
)

type tellRecord struct {
	RID  string   `json:"rid"`
	Time int64    `json:"time"`
	Type string   `json:"type"`
	Msg  string   `json:"msg"`
	Imgs []string `json:"imgs"`
}

type heyinwuClient struct {
	baseURL    string
	username   string
	password   string
	token      string
	shopID     int
	httpClient *http.Client
}

type hywEnvelope struct {
	Code int            `json:"code"`
	Data jsontext.Value `json:"data"`
}

func (c *heyinwuClient) login(ctx context.Context) error {
	var data struct {
		Token  string `json:"token"`
		ShopID int    `json:"shop_id"`
	}
	if err := c.call(ctx, "login", map[string]any{"id": c.username, "pass": c.password, "token": ""}, &data); err != nil {
		return err
	}
	if data.Token == "" {
		return fmt.Errorf("login returned an empty token")
	}
	c.token, c.shopID = data.Token, data.ShopID
	return nil
}

func (c *heyinwuClient) call(ctx context.Context, endpoint string, payload map[string]any, result any) error {
	// This allowlist deliberately excludes every admin mutation, including note/done flags.
	switch endpoint {
	case "login", "shop_list", "tell_log_list", "tell_log_detail":
	default:
		return fmt.Errorf("admin endpoint is not read-only")
	}
	payload["token"] = c.token
	if endpoint == "login" {
		payload["token"] = ""
	}
	for attempt := range 2 {
		var envelope hywEnvelope
		if err := postJSON(ctx, c.httpClient, c.baseURL+"/"+endpoint, payload, &envelope); err != nil {
			return fmt.Errorf("%s: %w", endpoint, err)
		}
		if envelope.Code == 455 && endpoint != "login" && attempt == 0 {
			if err := c.login(ctx); err != nil {
				return err
			}
			payload["token"] = c.token
			continue
		}
		if envelope.Code != 200 {
			return fmt.Errorf("%s: API code %d", endpoint, envelope.Code)
		}
		if err := json.Unmarshal(envelope.Data, result); err != nil {
			return fmt.Errorf("decode %s: %w", endpoint, err)
		}
		return nil
	}
	return fmt.Errorf("admin session expired")
}

func (c *heyinwuClient) list(ctx context.Context, shopID, page, size int) ([]tellRecord, int, error) {
	var data struct {
		List  []tellRecord `json:"list"`
		Total int          `json:"total"`
	}
	err := c.call(ctx, "tell_log_list", map[string]any{"shop_id": shopID, "page": page, "size": size, "kw": ""}, &data)
	return data.List, data.Total, err
}

func (c *heyinwuClient) detail(ctx context.Context, shopID int, rid string) (tellRecord, error) {
	var record tellRecord
	err := c.call(ctx, "tell_log_detail", map[string]any{"shop_id": shopID, "rid": rid}, &record)
	if err == nil && record.RID != rid {
		err = fmt.Errorf("detail returned a different record ID")
	}
	return record, err
}
