package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

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
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.APIKey))
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		writeErr(w, 400, "连接失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 4096))
		detail := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusUnauthorized {
			detail = "API Key 被上游拒绝（401）。请确认密钥属于该 Base URL 对应的服务商，且未失效。"
			if strings.TrimSpace(string(body)) != "" { detail += " 上游信息: " + strings.TrimSpace(string(body)) }
		}
		writeErr(w, 400, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail))
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
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(req.APIKey))
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		writeErr(w, 400, "无法连接: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusUnauthorized {
			writeErr(w, 400, "API Key 被上游拒绝（401）。请核对服务商、Base URL 与密钥是否匹配。")
			return
		}
		writeErr(w, 400, fmt.Sprintf("上游返回 HTTP %d: %s", resp.StatusCode, detail))
		return
	}
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
