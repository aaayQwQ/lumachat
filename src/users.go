package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

func handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeErr(w, 400, "账号密码不能为空")
		return
	}
	if len(req.Username) > 32 || len(req.Password) < 4 {
		writeErr(w, 400, "用户名最多32字符，密码至少4字符")
		return
	}
	if matched, _ := regexp.MatchString(`^[a-zA-Z0-9_\p{Han}]+$`, req.Username); !matched {
		writeErr(w, 400, "用户名只能包含字母数字下划线中文")
		return
	}

	mu.Lock()
	defer mu.Unlock()
	if _, exists := users[req.Username]; exists {
		writeErr(w, 400, "用户已存在")
		return
	}

	sessID := genID("sess")
	provName, modelName := "", ""
	for k, p := range providers {
		provName = k
		if len(p.Models) > 0 {
			modelName = p.Models[0]
		}
		break
	}
	users[req.Username] = &User{
		Username:     req.Username,
		PasswordHash: hashPassword(req.Password),
		Sessions: map[string]*ChatSession{
			sessID: {ID: sessID, Title: "新对话", Messages: []Message{}, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		},
		ActiveSessID:  sessID,
		ShowTokens:    true,
		SelectedProv:  provName,
		SelectedModel: modelName,
		Theme:         "light",
		CreatedAt:     time.Now(),
	}
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "注册成功"})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	mu.RLock()
	u, exists := users[req.Username]
	ok := exists && u.PasswordHash == hashPassword(req.Password)
	mu.RUnlock()
	if !ok {
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	token := genID("auth")
	authMu.Lock()
	authTokens[token] = u.Username
	authMu.Unlock()
	writeJSON(w, 200, APIResp{Code: 0, Data: map[string]string{
		"username": u.Username,
		"theme":    u.Theme,
		"token":    token,
	}})
}

func handleChangePassword(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	if len(req.NewPassword) < 4 {
		writeErr(w, 400, "新密码至少4字符")
		return
	}
	mu.Lock()
	u, ok := users[username]
	if !ok || u.PasswordHash != hashPassword(req.OldPassword) {
		mu.Unlock()
		writeErr(w, 400, "原密码错误")
		return
	}
	u.PasswordHash = hashPassword(req.NewPassword)
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "密码已修改"})
}

func handleUserState(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	mu.RLock()
	u, exists := users[username]
	if !exists {
		mu.RUnlock()
		writeErr(w, 401, "未授权")
		return
	}
	data, _ := json.Marshal(u)
	mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		SelectedProv  string `json:"selected_prov"`
		SelectedModel string `json:"selected_model"`
		ShowTokens    bool   `json:"show_tokens"`
		DisableThink  bool   `json:"disable_think"`
		DisableWeb    bool   `json:"disable_web"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	mu.Lock()
	if u, ok := users[username]; ok {
		u.SelectedProv = req.SelectedProv
		u.SelectedModel = req.SelectedModel
		u.ShowTokens = req.ShowTokens
		u.DisableThink = req.DisableThink
		u.DisableWeb = req.DisableWeb
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "已保存"})
}

func handleUpdateTheme(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		Theme string `json:"theme"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	mu.Lock()
	if u, ok := users[username]; ok {
		u.Theme = req.Theme
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0})
}
