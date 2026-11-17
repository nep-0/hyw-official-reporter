// hyw-official-reporter forwards Heyinwu broadcast records to an official QQ bot.
package main

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func loadEnv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid dotenv assignment")
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func main() {
	if err := run(); err != nil {
		slog.Error("reporter stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "watch", "fetch (read-only preview), shops, or watch")
	envPath := flag.String("env", ".env", "dotenv file; process environment takes precedence")
	statePath := flag.String("state", "data/state.json", "local delivery checkpoint")
	interval := flag.Duration("interval", 3*time.Second, "polling interval")
	flag.Parse()
	if err := loadEnv(*envPath); err != nil {
		return err
	}
	if *mode != "fetch" && *mode != "shops" && *mode != "watch" {
		return fmt.Errorf("unknown mode %q", *mode)
	}
	if *interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	source := &heyinwuClient{baseURL: "https://wechat.heyinwu.top/api/web", username: os.Getenv("HYW_ADMIN_USERNAME"), password: os.Getenv("HYW_ADMIN_PASSWORD"), httpClient: client}
	if source.username == "" || source.password == "" {
		return fmt.Errorf("HYW_ADMIN_USERNAME and HYW_ADMIN_PASSWORD are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := source.login(ctx); err != nil {
		return err
	}
	if *mode == "shops" {
		var shops []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		if err := source.call(ctx, "shop_list", map[string]any{}, &shops); err != nil {
			return err
		}
		return json.MarshalWrite(os.Stdout, shops)
	}
	shopID := source.shopID
	if raw := os.Getenv("HYW_SHOP_ID"); raw != "" {
		var err error
		shopID, err = strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("HYW_SHOP_ID must be an integer")
		}
	}
	if shopID <= 0 {
		return fmt.Errorf("HYW_SHOP_ID must select a shop")
	}
	if *mode == "fetch" {
		records, total, err := source.list(ctx, shopID, 1, 50)
		if err != nil {
			return err
		}
		slog.Info("fetched broadcast records", "shop_id", shopID, "total", total, "page_size", len(records))
		for _, record := range records {
			detail, err := source.detail(ctx, shopID, record.RID)
			if err != nil {
				return err
			}
			if err := json.MarshalWrite(os.Stdout, detail); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(os.Stdout); err != nil {
				return err
			}
		}
		return nil
	}
	appID, secret, groupID := os.Getenv("QQ_BOT_APPID"), os.Getenv("QQ_BOT_SECRET"), os.Getenv("QQ_BOT_GROUP_OPENID")
	if appID == "" || secret == "" || groupID == "" {
		return fmt.Errorf("watch requires QQ_BOT_APPID, QQ_BOT_SECRET, and QQ_BOT_GROUP_OPENID")
	}
	bot := newQQClient(appID, secret, groupID)
	// Exclusive file creation prevents two local processes from reporting the same records.
	if err := os.MkdirAll(filepath.Dir(*statePath), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(*statePath+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("acquire state lock (remove stale lock after a crash): %w", err)
	}
	defer func() {
		// Closing the lock does not write data; removing it allows the next process to start.
		if err := lock.Close(); err != nil {
			slog.Warn("close state lock", "error", err)
		}
		if err := os.Remove(*statePath + ".lock"); err != nil {
			slog.Warn("remove state lock", "error", err)
		}
	}()
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	return watchPolls(ctx, ticker.C, func() error {
		return runPoll(ctx, source, shopID, bot.groupID, *statePath, bot.send)
	})
}

func watchPolls(ctx context.Context, ticks <-chan time.Time, poll func() error) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := poll(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("poll failed; retrying on next interval", "error", err)
		} else {
			slog.Info("poll complete")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
		}
	}
}
