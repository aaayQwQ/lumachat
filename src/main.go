package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var bufPool = sync.Pool{New: func() interface{} { return new(bytes.Buffer) }}

type middleware func(http.HandlerFunc) http.HandlerFunc

func chain(h http.HandlerFunc, mws ...middleware) http.HandlerFunc {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

func mwRecover(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[panic] %s %s: %v", r.Method, r.URL.Path, rec)
				writeErr(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next(w, r)
	}
}

func mwLog(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		atomic.AddInt64(&statRequests, 1)
		next(w, r)
		log.Printf("[req] %s %s %v", r.Method, r.URL.Path, time.Since(start))
	}
}

func mwRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		rateMu.Lock()
		b, ok := rateTable[ip]
		if !ok || time.Now().After(b.ResetAt) {
			b = &rateBucket{Count: 0, ResetAt: time.Now().Add(time.Minute)}
			rateTable[ip] = b
		}
		b.Count++
		over := b.Count > 120
		rateMu.Unlock()
		if over {
			writeErr(w, http.StatusTooManyRequests, "请求过于频繁")
			return
		}
		next(w, r)
	}
}

func mwAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "未登录或令牌无效")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		authMu.RLock()
		username, ok := authTokens[token]
		authMu.RUnlock()
		if !ok || username == "" || r.Header.Get("X-User") != username {
			writeErr(w, http.StatusUnauthorized, "登录状态无效，请重新登录")
			return
		}
		mu.RLock()
		_, exists := users[username]
		mu.RUnlock()
		if !exists {
			writeErr(w, http.StatusUnauthorized, "用户不存在")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIResp{Code: status, Message: msg})
}

func clientIP(r *http.Request) string {
	// X-Forwarded-For is intentionally ignored unless a trusted-proxy policy is configured.
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}

// ==================== 内嵌网页模板 ====================

var htmlTemplate string

func loadTemplate() {
	htmlTemplate = htmlTemplateContent
	log.Printf("[web] 已加载内嵌模板 (%d 字节)", len(htmlTemplate))
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// ==================== 主入口 ====================

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("LumaChat v%s 启动中...", version)

	loadTemplate()

	mu.Lock()
	loadProvidersUnsafe()
	loadUsersUnsafe()
	mu.Unlock()

	stop := make(chan struct{})
	go watchConfig(stop)
	go saveUsersWorker(stop)

	mux := http.NewServeMux()
	mux.HandleFunc("/", chain(handleIndex, mwRecover, mwLog))
	mux.HandleFunc("/api/health", chain(handleHealth, mwRecover, mwLog))
	mux.HandleFunc("/api/register", chain(handleRegister, mwRecover, mwLog, mwRateLimit))
	mux.HandleFunc("/api/login", chain(handleLogin, mwRecover, mwLog, mwRateLimit))
	mux.HandleFunc("/api/user/state", chain(handleUserState, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/user/settings", chain(handleUpdateSettings, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/user/theme", chain(handleUpdateTheme, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/user/password", chain(handleChangePassword, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/chat", chain(handleChatStream, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/providers", chain(handleProviders, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/providers/add", chain(handleAddProvider, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/providers/update", chain(handleUpdateProvider, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/session/preferences", chain(handleSessionPreferences, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/providers/delete", chain(handleDeleteProvider, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/providers/test", chain(handleTestProvider, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/models/fetch", chain(handleFetchModels, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/session/new", chain(handleNewSession, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/session/switch", chain(handleSwitchSession, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/session/rename", chain(handleRenameSession, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/session/delete", chain(handleDeleteSession, mwRecover, mwLog, mwAuth))
	mux.HandleFunc("/api/session/clear", chain(handleClearSession, mwRecover, mwLog, mwAuth))

	server := &http.Server{
		Addr:         "0.0.0.0:8080",
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("收到退出信号，正在关闭...")
		close(stop)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		log.Println("已退出")
	}()

	log.Printf("监听 http://0.0.0.0:8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("启动失败: %v", err)
	}
}

// ==================== 基础 API ====================

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(htmlTemplate))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	pc := len(providers)
	uc := len(users)
	mu.RUnlock()
	writeJSON(w, 200, APIResp{Code: 0, Data: map[string]interface{}{
		"version":   version,
		"uptime":    time.Since(startTime).String(),
		"providers": pc,
		"users":     uc,
		"requests":  atomic.LoadInt64(&statRequests),
		"chats":     atomic.LoadInt64(&statChats),
		"tokens":    atomic.LoadInt64(&statTokens),
		"timestamp": time.Now().Format(time.RFC3339),
	}})
}

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

func handleProviders(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	list := make([]Provider, 0, len(providers))
	for _, p := range providers {
		masked := p
		if len(p.APIKey) > 12 {
			masked.APIKey = p.APIKey[:6] + "..." + p.APIKey[len(p.APIKey)-4:]
		} else if p.APIKey != "" {
			masked.APIKey = "****"
		}
		list = append(list, masked)
	}
	mu.RUnlock()
	writeJSON(w, 200, list)
}

func handleAddProvider(w http.ResponseWriter, r *http.Request) {
	var p Provider
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	if p.Name == "" || p.BaseURL == "" || len(p.Models) == 0 {
		writeErr(w, 400, "名称、URL、模型不能为空")
		return
	}
	if !strings.HasPrefix(p.BaseURL, "http://") && !strings.HasPrefix(p.BaseURL, "https://") {
		writeErr(w, 400, "BaseURL 必须以 http:// 或 https:// 开头")
		return
	}
	mu.Lock()
	if _, exists := providers[p.Name]; exists {
		mu.Unlock()
		writeErr(w, 400, "服务商已存在，请使用其他名称")
		return
	}
	p.CreatedAt = time.Now()
	providers[p.Name] = p
	saveConfigUnsafe()
	mu.Unlock()
	writeJSON(w, 200, APIResp{Code: 0, Message: "添加成功"})
}

func handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OriginalName string `json:"original_name"`
		Name string `json:"name"`
		BaseURL string `json:"base_url"`
		APIKey string `json:"api_key"`
		Models []string `json:"models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	req.OriginalName = strings.TrimSpace(req.OriginalName); req.Name = strings.TrimSpace(req.Name); req.BaseURL = strings.TrimSpace(req.BaseURL)
	if req.OriginalName == "" || req.Name == "" || req.BaseURL == "" || len(req.Models) == 0 { writeErr(w, 400, "名称、URL、模型不能为空"); return }
	if !strings.HasPrefix(req.BaseURL, "http://") && !strings.HasPrefix(req.BaseURL, "https://") { writeErr(w, 400, "BaseURL 必须以 http:// 或 https:// 开头"); return }
	mu.Lock(); old, ok := providers[req.OriginalName]
	if !ok { mu.Unlock(); writeErr(w, 404, "服务商不存在"); return }
	if req.Name != req.OriginalName { if _, exists := providers[req.Name]; exists { mu.Unlock(); writeErr(w, 400, "新名称已被使用"); return }; delete(providers, req.OriginalName) }
	if strings.TrimSpace(req.APIKey) == "" || strings.Contains(req.APIKey, "...") || req.APIKey == "****" { req.APIKey = old.APIKey }
	providers[req.Name] = Provider{Name:req.Name, BaseURL:req.BaseURL, APIKey:req.APIKey, Models:req.Models, CreatedAt:old.CreatedAt}; saveConfigUnsafe()
	for _, u := range users { if u.SelectedProv == req.OriginalName { u.SelectedProv = req.Name; if !containsString(req.Models, u.SelectedModel) { u.SelectedModel = req.Models[0] } } }
	mu.Unlock(); saveUsersDebounced(); writeJSON(w, 200, APIResp{Code:0, Message:"服务商已更新"})
}
func containsString(items []string, target string) bool { for _, item := range items { if item == target { return true } }; return false }

func handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" {
		writeErr(w, 400, "缺少 name")
		return
	}
	mu.Lock()
	if _, exists := providers[req.Name]; !exists {
		mu.Unlock()
		writeErr(w, 404, "服务商不存在")
		return
	}
	delete(providers, req.Name)
	saveConfigUnsafe()
	for _, u := range users {
		if u.SelectedProv == req.Name {
			u.SelectedProv = ""
			u.SelectedModel = ""
			for k, p := range providers {
				u.SelectedProv = k
				if len(p.Models) > 0 {
					u.SelectedModel = p.Models[0]
				}
				break
			}
		}
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "已删除"})
}

func handleTestProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.BaseURL == "" {
		writeErr(w, 400, "base_url 为空")
		return
	}
	targetURL := strings.TrimSuffix(req.BaseURL, "/") + "/models"
	httpReq, _ := http.NewRequestWithContext(r.Context(), "GET", targetURL, nil)
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		writeErr(w, 400, "连接失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		writeErr(w, 400, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)))
		return
	}
	writeJSON(w, 200, APIResp{Code: 0, Message: "连接成功"})
}

func handleFetchModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.BaseURL == "" {
		writeErr(w, 400, "base_url 为空")
		return
	}
	targetURL := strings.TrimSuffix(req.BaseURL, "/") + "/models"
	httpReq, _ := http.NewRequestWithContext(r.Context(), "GET", targetURL, nil)
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		writeErr(w, 400, "无法连接: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	models := []string{}
	if err := json.Unmarshal(body, &result); err == nil {
		for _, m := range result.Data {
			models = append(models, m.ID)
		}
	}
	writeJSON(w, 200, models)
}

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

func handleChatStream(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-User")
	var req struct {
		SessionID string `json:"session_id"`
		Prompt    string `json:"prompt"`
		ThinkEnabled *bool `json:"think_enabled"`
		ShowTokens *bool `json:"show_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "参数错误")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeErr(w, 400, "prompt 为空")
		return
	}

	mu.Lock()
	u, exists := users[username]
	if !exists {
		mu.Unlock()
		writeErr(w, 401, "未授权")
		return
	}
	prov, provOk := providers[u.SelectedProv]
	if !provOk {
		mu.Unlock()
		writeErr(w, 400, "服务商不存在，请在设置中添加")
		return
	}
	sid := req.SessionID
	if sid == "" {
		sid = u.ActiveSessID
	}
	sess, ok := u.Sessions[sid]
	if !ok || sess == nil {
		mu.Unlock()
		writeErr(w, 400, "会话不存在")
		return
	}

	sess.Messages = append(sess.Messages, Message{
		Role: "user", Content: req.Prompt, Timestamp: time.Now(),
	})
	if sess.Title == "新对话" || sess.Title == "" {
		t := []rune(req.Prompt)
		if len(t) > 12 {
			sess.Title = string(t[:12]) + "..."
		} else {
			sess.Title = string(t)
		}
	}
	sess.UpdatedAt = time.Now()

	if req.ThinkEnabled != nil { v := *req.ThinkEnabled; sess.ThinkEnabled = &v }
	if req.ShowTokens != nil { v := *req.ShowTokens; sess.ShowTokens = &v }
	apiMsgs := make([]Message, len(sess.Messages))
	copy(apiMsgs, sess.Messages)
	model := u.SelectedModel
	showTokens := u.ShowTokens
	if sess.ShowTokens != nil { showTokens = *sess.ShowTokens }
	thinkEnabled := !u.DisableThink
	if sess.ThinkEnabled != nil { thinkEnabled = *sess.ThinkEnabled }
	disableThink := !thinkEnabled
	disableWeb := u.DisableWeb
	mu.Unlock()

	saveUsersDebounced()
	incChats()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	openaiReq := map[string]interface{}{
		"model":          model,
		"messages":       apiMsgs,
		"stream":         true,
		"stream_options": map[string]interface{}{"include_usage": true},
	}
	// DeepSeek-style OpenAI-compatible APIs use a top-level thinking switch.
	thinkType := "enabled"
	if disableThink { thinkType = "disabled" }
	openaiReq["thinking"] = map[string]string{"type": thinkType}
	// Keep provider-specific search controls separate from the thinking switch.
	opts := map[string]interface{}{}
	if disableWeb { opts["search"] = false }
	if len(opts) > 0 { openaiReq["extra_body"] = opts }

	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	_ = json.NewEncoder(buf).Encode(openaiReq)
	bodyBytes := append([]byte(nil), buf.Bytes()...)
	bufPool.Put(buf)

	targetURL := strings.TrimSuffix(prov.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(r.Context(), "POST", targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", "上游请求失败，请检查服务商地址和网络")
		if canFlush {
			flusher.Flush()
		}
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+prov.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(httpReq)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", "上游请求失败，请检查服务商地址和网络")
		if canFlush {
			flusher.Flush()
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode))
		if canFlush {
			flusher.Flush()
		}
		return
	}

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var fullReply strings.Builder
	var pTok, cTok int

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data: ") {
				data := line[6:]
				if data == "[DONE]" {
					break
				}
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
					Usage struct {
						PromptTokens     int `json:"prompt_tokens"`
						CompletionTokens int `json:"completion_tokens"`
					} `json:"usage"`
				}
				if json.Unmarshal([]byte(data), &chunk) == nil {
					if chunk.Usage.PromptTokens > 0 {
						pTok = chunk.Usage.PromptTokens
						cTok = chunk.Usage.CompletionTokens
					}
					if len(chunk.Choices) > 0 {
						if c := chunk.Choices[0].Delta.Content; c != "" {
							fullReply.WriteString(c)
							esc := strings.ReplaceAll(c, "\n", "\ndata: ")
							fmt.Fprintf(w, "data: %s\n\n", esc)
							if canFlush {
								flusher.Flush()
							}
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	reply := fullReply.String()
	if pTok+cTok > 0 {
		addTokens(int64(pTok + cTok))
	}

	mu.Lock()
	if u2, ok := users[username]; ok {
		if s, ok := u2.Sessions[sid]; ok {
			s.Messages = append(s.Messages, Message{
				Role: "assistant", Content: reply, Timestamp: time.Now(),
			})
			s.UpdatedAt = time.Now()
		}
	}
	mu.Unlock()
	saveUsersDebounced()

	if showTokens && pTok > 0 {
		fmt.Fprintf(w, "event: usage\ndata: %d,%d\n\n", pTok, cTok)
		if canFlush {
			flusher.Flush()
		}
	}
	fmt.Fprintf(w, "event: done\ndata: [DONE]\n\n")
	if canFlush {
		flusher.Flush()
	}
}
