package main

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type reportState struct {
	ShopID  int             `json:"shop_id"`
	GroupID string          `json:"group_id"`
	Seen    map[string]bool `json:"seen"`
}

func saveState(path string, state reportState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".report-state-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name) // Successful rename removes the temporary name.
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func runPoll(ctx context.Context, source *heyinwuClient, shopID int, groupID, statePath string, send func(context.Context, tellRecord) error) error {
	state := reportState{ShopID: shopID, GroupID: groupID, Seen: map[string]bool{}}
	body, err := os.ReadFile(statePath)
	baseline := errors.Is(err, os.ErrNotExist)
	if err != nil && !baseline {
		return err
	}
	if !baseline {
		if err := json.Unmarshal(body, &state); err != nil {
			return fmt.Errorf("decode state: %w", err)
		}
		if state.ShopID != shopID || state.GroupID != groupID || state.Seen == nil {
			return fmt.Errorf("state belongs to another shop/group or is invalid")
		}
	}
	var pending []tellRecord
	// The API is newest first; scan all retained pages so downtime never skips a backlog.
	current := map[string]bool{}
	for page := 1; ; page++ {
		records, total, err := source.list(ctx, shopID, page, 50)
		if err != nil {
			return err
		}
		if len(records) == 0 && (page-1)*50 < total {
			return fmt.Errorf("broadcast pagination ended before total")
		}
		for _, record := range records {
			if record.RID == "" {
				return fmt.Errorf("broadcast record has empty ID")
			}
			if current[record.RID] {
				continue
			}
			current[record.RID] = true
			if !state.Seen[record.RID] {
				pending = append(pending, record)
			}
		}
		if page*50 >= total {
			break
		}
	}
	if baseline {
		state.Seen = current
		return saveState(statePath, state)
	}
	slices.SortFunc(pending, func(a, b tellRecord) int {
		if order := cmp.Compare(a.Time, b.Time); order != 0 {
			return order
		}
		return cmp.Compare(a.RID, b.RID)
	})
	for _, record := range pending {
		detail, err := source.detail(ctx, shopID, record.RID)
		if err != nil {
			return err
		}
		if err := send(ctx, detail); err != nil {
			return fmt.Errorf("deliver record %s: %w", record.RID, err)
		}
		state.Seen[record.RID] = true
		if err := saveState(statePath, state); err != nil {
			return fmt.Errorf("save delivered record: %w", err)
		}
	}
	// Discard IDs that have aged out of the server's 30-day retention window.
	state.Seen = current
	return saveState(statePath, state)
}
