package main

import (
	"fmt"
	"net/url"
	"strings"
)

type cqImagePolicy map[string]bool

func newCQImagePolicy(extra string) (cqImagePolicy, error) {
	policy := cqImagePolicy{"heyinwu.top": true, "wechat.heyinwu.top": true}
	for host := range strings.SplitSeq(extra, ",") {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		u, err := url.Parse("https://" + host)
		if err != nil || u.Hostname() != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(host, "#") {
			return nil, fmt.Errorf("HYW_WS_IMAGE_HOSTS must contain bare hostnames")
		}
		policy[host] = true
	}
	return policy, nil
}

var cqTextUnescape = strings.NewReplacer("&#91;", "[", "&#93;", "]", "&amp;", "&")
var cqParamUnescape = strings.NewReplacer("&#44;", ",", "&#91;", "[", "&#93;", "]", "&amp;", "&")

func parseCQBroadcast(message string, autoEscape bool, policy cqImagePolicy) (tellRecord, error) {
	if message == "" {
		return tellRecord{}, fmt.Errorf("empty broadcast message")
	}
	if autoEscape {
		return tellRecord{Msg: message}, nil
	}
	if !strings.Contains(message, "[CQ:") {
		return tellRecord{Msg: cqTextUnescape.Replace(message)}, nil
	}
	var text strings.Builder
	var record tellRecord
	for {
		prefix, rest, found := strings.Cut(message, "[CQ:")
		text.WriteString(cqTextUnescape.Replace(prefix))
		if !found {
			break
		}
		tag, tail, closed := strings.Cut(rest, "]")
		kind, fields, hasFields := strings.Cut(tag, ",")
		if !closed || !hasFields || kind != "image" {
			return tellRecord{}, fmt.Errorf("unsupported or malformed CQ tag")
		}
		var sources [3]string // NapCat prefers url, then file, then path.
		var seen [3]bool
		for field := range strings.SplitSeq(fields, ",") {
			key, value, ok := strings.Cut(field, "=")
			if !ok || key == "" {
				return tellRecord{}, fmt.Errorf("malformed CQ image parameter")
			}
			var index int
			switch key {
			case "url":
				index = 0
			case "file":
				index = 1
			case "path":
				index = 2
			default:
				continue
			}
			if seen[index] {
				return tellRecord{}, fmt.Errorf("duplicate CQ image %s parameter", key)
			}
			seen[index] = true
			sources[index] = cqParamUnescape.Replace(value)
		}
		var file string
		for _, source := range sources {
			if strings.TrimSpace(source) != "" {
				file = source
				break
			}
		}
		if file == "" {
			return tellRecord{}, fmt.Errorf("CQ image has no nonblank url, file, or path parameter")
		}
		if strings.HasPrefix(file, "base64://") || strings.HasPrefix(file, "data:") {
			return tellRecord{}, fmt.Errorf("CQ image uses unsupported inline image data")
		}
		u, err := url.Parse(file)
		if err != nil {
			return tellRecord{}, fmt.Errorf("CQ image file contains a malformed URL")
		}
		host := strings.ToLower(u.Hostname())
		if (u.Scheme != "https" && u.Scheme != "http") || host == "" {
			return tellRecord{}, fmt.Errorf("CQ image file is not an absolute HTTP(S) URL")
		}
		if u.User != nil || u.Port() != "" {
			return tellRecord{}, fmt.Errorf("CQ image URL contains user information or an explicit port")
		}
		if !policy[host] {
			return tellRecord{}, fmt.Errorf("CQ image host %q is not allowed; configure HYW_WS_IMAGE_HOSTS for trusted hosts", host)
		}
		record.Imgs = append(record.Imgs, file)
		message = tail
	}
	record.Msg = text.String()
	return record, nil
}
