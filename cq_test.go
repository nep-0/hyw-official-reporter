package main

import (
	"strings"
	"testing"
)

func TestCQImageSourceRejection(t *testing.T) {
	policy, err := newCQImagePolicy("")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fields, hint string
	}{
		{"missing source", "cache=0", "no nonblank url, file, or path parameter"},
		{"inline data", "file=base64://sensitiveValue", "inline image data"},
		{"malformed URL", "file=https://bad host/sensitiveValue", "malformed URL"},
		{"relative path", "file=/sensitiveValue", "not an absolute HTTP(S) URL"},
		{"blocked host", "file=https://images.example.org/sensitiveValue", "images.example.org"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCQBroadcast("[CQ:image,"+tc.fields+"]", false, policy)
			if err == nil || !strings.Contains(err.Error(), tc.hint) {
				t.Fatalf("expected rejection with hint %q, got %v", tc.hint, err)
			}
			if strings.Contains(err.Error(), "sensitiveValue") {
				t.Fatal("rejection exposed the image source")
			}
		})
	}
}

func TestCQImageSourceSelection(t *testing.T) {
	policy, err := newCQImagePolicy("")
	if err != nil {
		t.Fatal(err)
	}
	const image = "https://wechat.heyinwu.top/example.png"
	for _, fields := range []string{
		"url=" + image,
		"file=example.png,url=" + image,
		"url=,file=" + image + ",path=ignored",
		"path=" + image,
	} {
		record, err := parseCQBroadcast("notice[CQ:image,"+fields+"]", false, policy)
		if err != nil || record.Msg != "notice" || len(record.Imgs) != 1 || record.Imgs[0] != image {
			t.Errorf("image fields %q: got %+v, %v; want notice and %s", fields, record, err, image)
		}
	}
}
