package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"\n\t"net/url"
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
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil { writeErr(w, 400, "参数错误"); return }
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	p.APIKey = strings.TrimSpace(p.APIKey)
	p.Models = cleanModels(p.Models)
	if p.Name == "" || p.BaseURL == "" || len(p.Models) == 0 { writeErr(w, 400, "名称、Base URL 和至少一个模型为必填项"); return }
	if !validBaseURL(p.BaseURL) { writeErr(w, 400, "Base URL 必须是有效的 http:// 或 https:// API 根地址"); return }
	mu.Lock()
	if _, exists := providers[p.Name]; exists { mu.Unlock(); writeErr(w, 409, "服务商名称已存在"); return }
	p.CreatedAt = time.Now()
	providers[p.Name] = p
	if err := saveConfigUnsafe(); err != nil { delete(providers, p.Name); mu.Unlock(); writeErr(w, 500, "服务商保存失败: "+err.Error()); return }
	for _, u := range users {
		if u != nil && u.SelectedProv == "" && len(p.Models) > 0 { u.SelectedProv = p.Name; u.SelectedModel = p.Models[0] }
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "自定义服务商已添加"})
}

func handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OriginalName string   `json:"original_name"`
		Name         string   `json:"name"`
		BaseURL      string   `json:"base_url"`
		APIKey       string   `json:"api_key"`
		Models       []string `json:"models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	req.OriginalName = strings.TrimSpace(req.OriginalName)
	req.Name = strings.TrimSpace(req.Name)
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Models = cleanModels(req.Models)
	if req.OriginalName == "" || req.Name == "" || req.BaseURL == "" || len(req.Models) == 0 { writeErr(w, 400, "名称、Base URL 和至少一个模型为必填项"); return }
	if !validBaseURL(req.BaseURL) { writeErr(w, 400, "Base URL 必须是有效的 http:// 或 https:// API 根地址"); return }

	mu.Lock()
	old, ok := providers[req.OriginalName]
	if !ok { mu.Unlock(); writeErr(w, 404, "服务商不存在"); return }
	if req.Name != req.OriginalName {
		if _, exists := providers[req.Name]; exists { mu.Unlock(); writeErr(w, 409, "新服务商名称已被使用"); return }
	}
	if req.APIKey == "" || req.APIKey == "****" || strings.Contains(req.APIKey, "...") { req.APIKey = old.APIKey }
	updated := Provider{Name: req.Name, BaseURL: req.BaseURL, APIKey: req.APIKey, Models: req.Models, CreatedAt: old.CreatedAt}
	type selection struct { prov, model string }
	oldSelections := make(map[string]selection)
	for name, u := range users { if u != nil { oldSelections[name] = selection{u.SelectedProv, u.SelectedModel} } }
	delete(providers, req.OriginalName)
	providers[req.Name] = updated
	for _, u := range users {
		if u != nil && u.SelectedProv == req.OriginalName {
			u.SelectedProv = req.Name
			if !containsString(req.Models, u.SelectedModel) { u.SelectedModel = req.Models[0] }
		}
	}
	if err := saveConfigUnsafe(); err != nil {
		delete(providers, req.Name)
		providers[req.OriginalName] = old
		for name, saved := range oldSelections { if u := users[name]; u != nil { u.SelectedProv, u.SelectedModel = saved.prov, saved.model } }
		mu.Unlock()
		writeErr(w, 500, "服务商更新保存失败: "+err.Error())
		return
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "服务商已更新"})
}
func containsString(items []string, target string) bool {
	for _, item := range items { if item == target { return true } }
	return false
}

func cleanModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model != "" && !seen[model] { seen[model] = true; out = append(out, model) }
	}
	return out
}

func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	var req struct { Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" { writeErr(w, 400, "缺少服务商名称"); return }
	mu.Lock()
	old, exists := providers[req.Name]
	if !exists { mu.Unlock(); writeErr(w, 404, "服务商不存在"); return }
	type selection struct { prov, model string }
	oldSelections := make(map[string]selection)
	for name, u := range users { if u != nil { oldSelections[name] = selection{u.SelectedProv, u.SelectedModel} } }
	delete(providers, req.Name)
	for _, u := range users { if u != nil && u.SelectedProv == req.Name { u.SelectedProv = ""; u.SelectedModel = "" } }
	if err := saveConfigUnsafe(); err != nil {
		providers[req.Name] = old
		for name, saved := range oldSelections { if u := users[name]; u != nil { u.SelectedProv, u.SelectedModel = saved.prov, saved.model } }
		mu.Unlock()
		writeErr(w, 500, "服务商删除保存失败: "+err.Error())
		return
	}
	mu.Unlock()
	saveUsersDebounced()
	writeJSON(w, 200, APIResp{Code: 0, Message: "自定义服务商已删除"})
}

func handleTestProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey string `json:"api_key"`
		ProviderName string `json:"provider_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.ProviderName = strings.TrimSpace(req.ProviderName)
	if req.APIKey == "" && req.ProviderName != "" {
		mu.RLock()
		if p, ok := providers[req.ProviderName]; ok && strings.TrimRight(strings.TrimSpace(p.BaseURL), "/") == req.BaseURL { req.APIKey = p.APIKey }
		mu.RUnlock()
	}
	if !validBaseURL(req.BaseURL) { writeErr(w, 400, "请填写有效的 Base URL（http:// 或 https://）"); return }
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, req.BaseURL+"/models", nil)
	if err != nil { writeErr(w, 400, "Base URL 无效: "+err.Error()); return }
	if req.APIKey != "" { httpReq.Header.Set("Authorization", "Bearer "+req.APIKey) }
	httpReq.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(httpReq)
	if err != nil { writeErr(w, 502, "连接失败: "+err.Error()); return }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 4096))
		detail := strings.TrimSpace(string(body)); if detail == "" { detail = http.StatusText(resp.StatusCode) }
		writeErr(w, 502, fmt.Sprintf("测试接口返回 HTTP %d: %s", resp.StatusCode, detail)); return
	}
	writeJSON(w, 200, APIResp{Code: 0, Message: "连接成功"})
}

func handleFetchModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey string `json:"api_key"`
		ProviderName string `json:"provider_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { writeErr(w, 400, "参数错误"); return }
	req.BaseURL = strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.ProviderName = strings.TrimSpace(req.ProviderName)
	if req.APIKey == "" && req.ProviderName != "" {
		mu.RLock()
		if p, ok := providers[req.ProviderName]; ok && strings.TrimRight(strings.TrimSpace(p.BaseURL), "/") == req.BaseURL { req.APIKey = p.APIKey }
		mu.RUnlock()
	}
	if !validBaseURL(req.BaseURL) { writeErr(w, 400, "请填写有效的 Base URL（http:// 或 https://）"); return }
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, req.BaseURL+"/models", nil)
	if err != nil { writeErr(w, 400, "Base URL 无效: "+err.Error()); return }
	if req.APIKey != "" { httpReq.Header.Set("Authorization", "Bearer "+req.APIKey) }
	httpReq.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(httpReq)
	if err != nil { writeErr(w, 502, "无法连接: "+err.Error()); return }
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil { writeErr(w, 502, "读取模型列表失败: "+err.Error()); return }
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusUnauthorized { detail = "API Key 被上游拒绝（401）。请核对密钥是否有效，以及密钥与 Base URL 是否来自同一服务商。" } else if detail == "" { detail = http.StatusText(resp.StatusCode) }
		writeErr(w, 502, fmt.Sprintf("上游返回 HTTP %d: %s", resp.StatusCode, detail)); return
	}
	var result struct { Data []struct { ID string `json:"id"` } `json:"data"` }
	if err := json.Unmarshal(body, &result); err != nil { writeErr(w, 502, "模型列表不是有效的 OpenAI 兼容格式（需要 data[].id）"); return }
	models := make([]string, 0, len(result.Data))
	for _, model := range result.Data { models = append(models, model.ID) }
	writeJSON(w, 200, cleanModels(models))
}
