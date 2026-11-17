package main

import (
	"bytes"
	"context"
	"crypto/md5" // QQ's upload protocol requires MD5 and SHA1, not for security.
	"crypto/sha1"
	"encoding/json/v2"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tencent-connect/botgo/errs"
)

const qqMaterialBase = "https://api.bot.qq.com/v2/media-images"
const maxImageBytes = 10 << 20

func (c *qqClient) materialRequest(ctx context.Context, method, path string, body, result any) error {
	data, err := c.api.Transport(ctx, method, qqMaterialBase+path, body)
	if err != nil {
		return err
	}
	if result != nil {
		return json.Unmarshal(data, result)
	}
	return nil
}

func (c *qqClient) uploadImage(ctx context.Context, source string) (string, int, int, error) {
	if len(c.pendingDeletes) >= 100 {
		return "", 0, 0, fmt.Errorf("one message exceeds QQ's 100-material quota")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", 0, 0, err
	}
	res, err := c.imageClient.Do(req)
	if err != nil {
		return "", 0, 0, fmt.Errorf("download image: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(res.Body, maxImageBytes+1))
	res.Body.Close() // The response is read-only; close errors do not affect its bytes.
	if res.StatusCode != http.StatusOK {
		return "", 0, 0, fmt.Errorf("image HTTP status %d", res.StatusCode)
	}
	if readErr != nil {
		return "", 0, 0, readErr
	}
	if len(data) > maxImageBytes {
		return "", 0, 0, fmt.Errorf("image exceeds QQ's 10 MiB limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, fmt.Errorf("decode image dimensions: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return "", 0, 0, fmt.Errorf("invalid image dimensions")
	}
	mime := "image/" + format
	extension := format
	if format == "jpeg" {
		extension = "jpg"
	}
	switch format {
	case "png", "jpeg", "gif":
	default:
		return "", 0, 0, fmt.Errorf("unsupported image format %q", format)
	}
	md5sum, sha1sum := md5.Sum(data), sha1.Sum(data)
	digest := fmt.Sprintf("%x", md5sum)
	var prepared struct {
		UploadID string `json:"upload_id"`
		URL      string `json:"presigned_url"`
	}
	err = c.materialRequest(ctx, http.MethodPost, "/prepare", map[string]any{"file_name": digest + "." + extension, "file_size": len(data), "content_type": mime, "md5": digest, "sha1": fmt.Sprintf("%x", sha1sum)}, &prepared)
	if err != nil {
		return "", 0, 0, fmt.Errorf("prepare material: %w", err)
	}
	parsed, err := url.Parse(prepared.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || prepared.UploadID == "" {
		return "", 0, 0, fmt.Errorf("invalid material upload response")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPut, prepared.URL, bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, err
	}
	// Never attach bot credentials to a presigned COS request.
	req.Header.Set("Content-Type", mime)
	res, err = c.imageClient.Do(req)
	if err != nil {
		return "", 0, 0, fmt.Errorf("upload material bytes: %w", err)
	}
	res.Body.Close() // No response body is required for a presigned upload.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", 0, 0, fmt.Errorf("material upload HTTP status %d", res.StatusCode)
	}
	etag := strings.Trim(res.Header.Get("ETag"), "\"")
	if !strings.EqualFold(etag, digest) {
		return "", 0, 0, fmt.Errorf("material upload ETag does not match image MD5")
	}
	var completed struct {
		ID  string `json:"image_id"`
		URL string `json:"url"`
	}
	err = c.materialRequest(ctx, http.MethodPost, "/complete", map[string]string{"upload_id": prepared.UploadID, "md5": etag}, &completed)
	// Track an ID even if the rest of the completion response is malformed.
	if completed.ID != "" {
		c.pendingDeletes = append(c.pendingDeletes, completed.ID)
	}
	if err != nil {
		return "", 0, 0, fmt.Errorf("complete material: %w", err)
	}
	if completed.ID == "" || !strings.HasPrefix(completed.URL, "qqbot-media://image/v1/") {
		return "", 0, 0, fmt.Errorf("invalid completed material response")
	}
	return completed.URL, config.Width, config.Height, nil
}

func (c *qqClient) cleanupMaterials(ctx context.Context) error {
	var failures []error
	remaining := c.pendingDeletes[:0]
	for _, id := range c.pendingDeletes {
		err := c.materialRequest(ctx, http.MethodDelete, "/"+url.PathEscape(id)+"/delete", nil, nil)
		if err != nil {
			var sdkErr *errs.Err
			var response struct {
				Code    int `json:"code"`
				ErrCode int `json:"err_code"`
			}
			if errors.As(err, &sdkErr) { // Already deleted is a successful cleanup.
				if decodeErr := json.Unmarshal([]byte(sdkErr.Text()), &response); decodeErr == nil && (response.Code == 40035008 || response.ErrCode == 40035008) {
					continue
				}
			}
			remaining = append(remaining, id)
			failures = append(failures, fmt.Errorf("delete material %s: %w", id, err))
		}
	}
	c.pendingDeletes = remaining
	return errors.Join(failures...)
}
