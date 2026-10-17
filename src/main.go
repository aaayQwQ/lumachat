package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

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
