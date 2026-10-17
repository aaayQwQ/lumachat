package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	configIni = "config.ini"
	userFile  = "users.json"
	version   = "1.0.0"
)

type Provider struct {
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	APIKey    string    `json:"api_key"`
	Models    []string  `json:"models"`
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type ChatSession struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Messages  []Message `json:"messages"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ThinkEnabled *bool `json:"think_enabled,omitempty"`
	ShowTokens *bool `json:"show_tokens,omitempty"`
}

type User struct {
	Username      string                  `json:"username"`
	PasswordHash  string                  `json:"password_hash"`
	Sessions      map[string]*ChatSession `json:"sessions"`
	ActiveSessID  string                  `json:"active_sess_id"`
	ShowTokens    bool                    `json:"show_tokens"`
	SelectedProv  string                  `json:"selected_prov"`
	SelectedModel string                  `json:"selected_model"`
	DisableThink  bool                    `json:"disable_think"`
	DisableWeb    bool                    `json:"disable_web"`
	Theme         string                  `json:"theme"`
	CreatedAt     time.Time               `json:"created_at"`
}

type APIResp struct {
	Code    int         `json:"code"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

type rateBucket struct {
	Count   int
	ResetAt time.Time
}

var (
	mu         sync.RWMutex
	providers  = make(map[string]Provider)
	users      = make(map[string]*User)
	authTokens = make(map[string]string)
	authMu     sync.RWMutex

	statRequests int64
	statChats    int64
	statTokens   int64

	rateMu    sync.Mutex
	rateTable = make(map[string]*rateBucket)

	startTime = time.Now()

	saveCh = make(chan struct{}, 1)
)

func genID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixNano(), hex.EncodeToString(b))
}

func hashPassword(pwd string) string {
	h := sha256.Sum256([]byte(pwd + "::nextchat::v2::salt"))
	return hex.EncodeToString(h[:])
}

func incChats()         { atomic.AddInt64(&statChats, 1) }
func addTokens(n int64) { atomic.AddInt64(&statTokens, n) }

func loadProvidersUnsafe() {
	if _, err := os.Stat(configIni); os.IsNotExist(err) {
		providers["DeepSeek官方"] = Provider{
			Name:      "DeepSeek官方",
			BaseURL:   "https://api.deepseek.com",
			APIKey:    "sk-your-key-here",
			Models:    []string{"deepseek-chat", "deepseek-reasoner"},
			CreatedAt: time.Now(),
		}
		saveConfigUnsafe()
		return
	}
	data, err := ioutil.ReadFile(configIni)
	if err != nil {
		log.Printf("[config] 读取失败: %v", err)
		return
	}
	newProviders := make(map[string]Provider)
	lines := strings.Split(string(data), "\n")
	var curr *Provider
	flush := func() {
		if curr != nil && curr.Name != "" {
			if curr.CreatedAt.IsZero() {
				curr.CreatedAt = time.Now()
			}
			newProviders[curr.Name] = *curr
		}
		curr = nil
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			curr = &Provider{}
			continue
		}
		if curr == nil {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k, v := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch k {
		case "name":
			curr.Name = v
		case "base_url":
			curr.BaseURL = v
		case "api_key":
			curr.APIKey = v
		case "models":
			for _, m := range strings.Split(v, ",") {
				if m = strings.TrimSpace(m); m != "" {
					curr.Models = append(curr.Models, m)
				}
			}
		}
	}
	flush()
	if len(newProviders) > 0 {
		providers = newProviders
		log.Printf("[config] 已加载 %d 个服务商", len(newProviders))
	}
}

func loadUsersUnsafe() {
	if _, err := os.Stat(userFile); os.IsNotExist(err) {
		return
	}
	data, err := ioutil.ReadFile(userFile)
	if err != nil {
		return
	}
	var loaded map[string]*User
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[config] 用户文件解析失败: %v", err)
		return
	}
	if loaded == nil {
		loaded = make(map[string]*User)
	}
	for _, u := range loaded {
		if u == nil {
			continue
		}
		if u.Sessions == nil {
			u.Sessions = make(map[string]*ChatSession)
		}
		if _, ok := u.Sessions[u.ActiveSessID]; !ok {
			for id := range u.Sessions {
				u.ActiveSessID = id
				break
			}
		}
		if u.Theme == "" {
			u.Theme = "light"
		}
	}
	users = loaded
	log.Printf("[config] 已加载 %d 个用户", len(loaded))
}

func saveConfigUnsafe() {
	var sb strings.Builder
	sb.WriteString("# LumaChat v" + version + " 服务商配置\n")
	sb.WriteString("# 修改后 2 秒内自动热加载\n\n")
	for _, p := range providers {
		sb.WriteString("[prov_" + p.Name + "]\n")
		sb.WriteString("name=" + p.Name + "\n")
		sb.WriteString("base_url=" + p.BaseURL + "\n")
		sb.WriteString("api_key=" + p.APIKey + "\n")
		sb.WriteString("models=" + strings.Join(p.Models, ",") + "\n\n")
	}
	if err := ioutil.WriteFile(configIni, []byte(sb.String()), 0600); err != nil {
		log.Printf("[config] 保存失败: %v", err)
	}
}

func saveUsersSync() {
	mu.Lock()
	data, err := json.MarshalIndent(users, "", "  ")
	mu.Unlock()
	if err != nil {
		log.Printf("[user] 序列化失败: %v", err)
		return
	}
	if err := ioutil.WriteFile(userFile, data, 0600); err != nil {
		log.Printf("[user] 写盘失败: %v", err)
	}
}

func saveUsersDebounced() {
	select {
	case saveCh <- struct{}{}:
	default:
	}
}

func saveUsersWorker(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			saveUsersSync()
			return
		case <-saveCh:
			time.Sleep(300 * time.Millisecond)
			saveUsersSync()
		}
	}
}

func watchConfig(stop <-chan struct{}) {
	var lastMod time.Time
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			fi, err := os.Stat(configIni)
			if err != nil {
				continue
			}
			if fi.ModTime().After(lastMod) {
				lastMod = fi.ModTime()
				mu.Lock()
				loadProvidersUnsafe()
				mu.Unlock()
			}
		}
	}
}
