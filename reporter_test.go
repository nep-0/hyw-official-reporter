package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestPollBaselineBacklogAndFailure(t *testing.T) {
	records := []tellRecord{{RID: "old", Time: 1}}
	detailCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tell_log_list":
			var request struct {
				Page int `json:"page"`
				Size int `json:"size"`
			}
			if err := json.UnmarshalRead(r.Body, &request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if request.Size != 20 {
				t.Errorf("fetch size = %d, want 20", request.Size)
			}
			start := min((request.Page-1)*request.Size, len(records))
			end := min(start+request.Size, len(records))
			if err := json.MarshalWrite(w, map[string]any{"code": 200, "data": map[string]any{"list": records[start:end], "total": len(records)}}); err != nil {
				t.Error(err)
			}
		case "/tell_log_detail":
			detailCalls++
			var request struct {
				RID string `json:"rid"`
			}
			if err := json.UnmarshalRead(r.Body, &request); err != nil {
				t.Error(err)
				return
			}
			if err := json.MarshalWrite(w, map[string]any{"code": 200, "data": tellRecord{RID: request.RID, Msg: request.RID}}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected admin write endpoint %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	source := &heyinwuClient{baseURL: server.URL, token: "session", httpClient: server.Client()}
	state := filepath.Join(t.TempDir(), "state.json")
	var delivered []string
	fail := false
	send := func(ctx context.Context, record tellRecord) error {
		if fail {
			return errors.New("delivery rejected")
		}
		delivered = append(delivered, record.RID)
		return nil
	}
	if err := runPoll(t.Context(), source, 45, "group", state, send); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 0 || detailCalls != 0 {
		t.Errorf("baseline delivered %v, detail calls %d", delivered, detailCalls)
	}
	for i := 1; i <= 55; i++ {
		records = append([]tellRecord{{RID: fmt.Sprint(i), Time: int64(i + 1)}}, records...)
	}
	fail = true
	if err := runPoll(t.Context(), source, 45, "group", state, send); err == nil {
		t.Fatal("expected rejected delivery")
	}
	fail = false
	if err := runPoll(t.Context(), source, 45, "group", state, send); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 55 || delivered[0] != "1" || delivered[54] != "55" {
		t.Errorf("delivery order/count = %v", delivered)
	}
	if err := runPoll(t.Context(), source, 45, "group", state, send); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 55 {
		t.Errorf("repeated poll delivered %d records, want 55", len(delivered))
	}
	if err := runPoll(t.Context(), source, 45, "different", state, send); err == nil {
		t.Error("expected group mismatch error")
	}
}
