package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const version = "1.0.0"

type Provider struct {
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	APIKey    string    `json:"api_key"`
	Models    []string  `json:"models"`
	ThinkingSwitch bool `json:"thinking_switch"`
	SearchExtraBody bool `json:"search_extra_body"`
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

