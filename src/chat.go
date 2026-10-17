package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"sync"
	"time"
)

var bufPool = sync.Pool{New: func() interface{} { return new(bytes.Buffer) }}

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
	// Bound persisted conversation history to prevent users.json and request memory growing forever.
	if len(sess.Messages) > 60 {
		sess.Messages = append([]Message(nil), sess.Messages[len(sess.Messages)-60:]...)
	}
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
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(prov.APIKey))
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
		detail, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
		if resp.StatusCode == http.StatusUnauthorized {
			msg = "API Key 认证失败（HTTP 401）：请检查密钥是否有效，并确认 Base URL 与密钥来自同一服务商。"
			if len(detail) > 0 { msg += " " + strings.TrimSpace(string(detail)) }
		} else if len(detail) > 0 {
			msg += ": " + strings.TrimSpace(string(detail))
		}
		msg = strings.ReplaceAll(strings.ReplaceAll(msg, "\\", "\\\\"), "\n", " ")
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", msg)
		if canFlush { flusher.Flush() }
		return
	}

	reader := bufio.NewReaderSize(resp.Body, 16*1024)
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
							if fullReply.Len() < 2<<20 {
								remaining := (2 << 20) - fullReply.Len()
								if len(c) > remaining { c = c[:remaining] }
								fullReply.WriteString(c)
							}
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
			if len(s.Messages) > 60 { s.Messages = append([]Message(nil), s.Messages[len(s.Messages)-60:]...) }
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
