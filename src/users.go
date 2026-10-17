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
	user, exists := users[username]
	if !exists || user == nil {
		mu.RUnlock()
		writeErr(w, http.StatusUnauthorized, "未授权")
		return
	}
	// Never expose password hashes to the browser; the stored User model is also used for persistence.
	view := struct {
		Username string `json:"username"`
		Sessions map[string]*ChatSession `json:"sessions"`
		ActiveSessID string `json:"active_sess_id"`
		ShowTokens bool `json:"show_tokens"`
		SelectedProv string `json:"selected_prov"`
		SelectedModel string `json:"selected_model"`
		DisableThink bool `json:"disable_think"`
		DisableWeb bool `json:"disable_web"`
		Theme string `json:"theme"`
		CreatedAt time.Time `json:"created_at"`
	}{
		Username: user.Username, Sessions: user.Sessions, ActiveSessID: user.ActiveSessID,
		ShowTokens: user.ShowTokens, SelectedProv: user.SelectedProv, SelectedModel: user.SelectedModel,
		DisableThink: user.DisableThink, DisableWeb: user.DisableWeb, Theme: user.Theme, CreatedAt: user.CreatedAt,
	}
	mu.RUnlock()
	writeJSON(w, http.StatusOK, view)
}

func handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		SelectedProv string `json:"selected_prov"`
		SelectedModel string `json:"selected_model"`
		ShowTokens bool `json:"show_tokens"`
		DisableThink bool `json:"disable_think"`
		DisableWeb bool `json:"disable_web"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	req.SelectedProv = strings.TrimSpace(req.SelectedProv)
	req.SelectedModel = strings.TrimSpace(req.SelectedModel)
	mu.Lock()
	user, ok := users[username]
	if !ok { mu.Unlock(); writeErr(w, 401, "用户不存在，请重新登录"); return }
	if req.SelectedProv == "" {
		if len(providers) > 0 { mu.Unlock(); writeErr(w, 400, "请先选择服务商"); return }
		req.SelectedModel = ""
	} else {
		provider, exists := providers[req.SelectedProv]
		if !exists { mu.Unlock(); writeErr(w, 400, "所选服务商不存在，请刷新页面后重试"); return }
		if !containsString(provider.Models, req.SelectedModel) { mu.Unlock(); writeErr(w, 400, "所选模型不属于当前服务商，请重新选择"); return }
	}
	user.SelectedProv, user.SelectedModel = req.SelectedProv, req.SelectedModel
	user.ShowTokens, user.DisableThink, user.DisableWeb = req.ShowTokens, req.DisableThink, req.DisableWeb
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "偏好设置已保存"})
}

func handleUpdateTheme(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct { Theme string `json:"theme"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	if req.Theme != "light" && req.Theme != "dark" { writeErr(w, 400, "theme 只能是 light 或 dark"); return }
	mu.Lock()
	user, ok := users[username]
	if !ok { mu.Unlock(); writeErr(w, 401, "用户不存在，请重新登录"); return }
	user.Theme = req.Theme
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "主题已保存"})
}
