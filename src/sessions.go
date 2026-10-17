package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func handleSessionPreferences(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct { SessionID string `json:"session_id"`; ThinkEnabled bool `json:"think_enabled"`; ShowTokens bool `json:"show_tokens"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	mu.Lock(); u, ok := users[username]; if !ok { mu.Unlock(); writeErr(w, 401, "未授权"); return }; s, ok := u.Sessions[req.SessionID]; if !ok || s == nil { mu.Unlock(); writeErr(w, 404, "会话不存在"); return }
	think, tokens := req.ThinkEnabled, req.ShowTokens; s.ThinkEnabled = &think; s.ShowTokens = &tokens; s.UpdatedAt = time.Now(); mu.Unlock(); saveUsersDebounced(); writeJSON(w, 200, APIResp{Code:0, Message:"已保存"})
}

func handleNewSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	mu.Lock()
	u, ok := users[username]
	if !ok {
		mu.Unlock()
		writeErr(w, 401, "未授权")
		return
	}
	newID := genID("sess")
	sess := &ChatSession{
		ID:        newID,
		Title:     "新对话",
		Messages:  []Message{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	u.Sessions[newID] = sess
	u.ActiveSessID = newID
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Data: sess})
}

func handleSwitchSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	mu.Lock()
	u, ok := users[username]
	if !ok {
		mu.Unlock()
		writeErr(w, 401, "未授权")
		return
	}
	if _, exists := u.Sessions[req.SessionID]; !exists {
		mu.Unlock()
		writeErr(w, 404, "会话不存在")
		return
	}
	u.ActiveSessID = req.SessionID
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0})
}

func handleRenameSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > 100 {
		writeErr(w, 400, "标题不能为空且最多100字符")
		return
	}
	mu.Lock()
	if u, ok := users[username]; ok {
		if s, exists := u.Sessions[req.SessionID]; exists {
			s.Title = req.Title
			s.UpdatedAt = time.Now()
		}
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0})
}

func handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	mu.Lock()
	u, ok := users[username]
	if !ok {
		mu.Unlock()
		writeErr(w, 401, "未授权")
		return
	}
	if _, exists := u.Sessions[req.SessionID]; !exists {
		mu.Unlock()
		writeErr(w, 404, "会话不存在")
		return
	}
	if len(u.Sessions) <= 1 {
		mu.Unlock()
		writeErr(w, 400, "至少保留一个会话")
		return
	}
	delete(u.Sessions, req.SessionID)
	if u.ActiveSessID == req.SessionID {
		for id := range u.Sessions {
			u.ActiveSessID = id
			break
		}
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0})
}

func handleClearSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	mu.Lock()
	if u, ok := users[username]; ok {
		if s, exists := u.Sessions[req.SessionID]; exists {
			s.Messages = []Message{}
			s.Title = "新对话"
			s.UpdatedAt = time.Now()
		}
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "已清空"})
}
