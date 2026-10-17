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
	mu.Lock()
	u, ok := users[username]
	if !ok { mu.Unlock(); writeErr(w, 401, "未授权"); return }
	session, ok := u.Sessions[req.SessionID]
	if !ok || session == nil { mu.Unlock(); writeErr(w, 404, "会话不存在"); return }
	think, tokens := req.ThinkEnabled, req.ShowTokens
	session.ThinkEnabled, session.ShowTokens = &think, &tokens
	session.UpdatedAt = time.Now()
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "会话设置已保存"})
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
	var req struct { SessionID string `json:"session_id"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	mu.Lock()
	u, ok := users[username]
	if !ok { mu.Unlock(); writeErr(w, 401, "未授权"); return }
	if _, exists := u.Sessions[req.SessionID]; !exists { mu.Unlock(); writeErr(w, 404, "会话不存在"); return }
	u.ActiveSessID = req.SessionID
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "已切换会话"})
}

func handleRenameSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct { SessionID string `json:"session_id"`; Title string `json:"title"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	req.Title = strings.TrimSpace(req.Title)
	if req.SessionID == "" || req.Title == "" || len([]rune(req.Title)) > 100 { writeErr(w, 400, "会话 ID 不能为空，标题不能为空且最多100个字符"); return }
	mu.Lock()
	u, ok := users[username]
	if !ok { mu.Unlock(); writeErr(w, 401, "未授权"); return }
	session, exists := u.Sessions[req.SessionID]
	if !exists || session == nil { mu.Unlock(); writeErr(w, 404, "会话不存在"); return }
	session.Title, session.UpdatedAt = req.Title, time.Now()
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "会话标题已保存"})
}

func handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct { SessionID string `json:"session_id"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	mu.Lock()
	u, ok := users[username]
	if !ok { mu.Unlock(); writeErr(w, 401, "未授权"); return }
	if _, exists := u.Sessions[req.SessionID]; !exists { mu.Unlock(); writeErr(w, 404, "会话不存在"); return }
	if len(u.Sessions) <= 1 { mu.Unlock(); writeErr(w, 400, "至少保留一个会话"); return }
	delete(u.Sessions, req.SessionID)
	if u.ActiveSessID == req.SessionID {
		u.ActiveSessID = ""
		for id := range u.Sessions { u.ActiveSessID = id; break }
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "会话已删除"})
}

func handleClearSession(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct { SessionID string `json:"session_id"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	mu.Lock()
	u, ok := users[username]
	if !ok { mu.Unlock(); writeErr(w, 401, "未授权"); return }
	session, exists := u.Sessions[req.SessionID]
	if !exists || session == nil { mu.Unlock(); writeErr(w, 404, "会话不存在"); return }
	session.Messages = []Message{}
	session.Title = "新对话"
	session.UpdatedAt = time.Now()
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "会话已清空"})
}
