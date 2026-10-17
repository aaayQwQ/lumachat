package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Persistent data is stored beside the executable, not in the working directory.
func configDirPath() string { return filepath.Join(exeDir(), "config") }
func providerConfigPath() string { return filepath.Join(configDirPath(), "config.ini") }
func userDataPath() string { return filepath.Join(configDirPath(), "users.json") }

func ensureConfigDir() error {
	dir := configDirPath()
	if err := os.MkdirAll(dir, 0700); err != nil { return err }
	// One-time migration from older releases that wrote data beside the executable.
	for _, name := range []string{"config.ini", "users.json"} {
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err == nil { continue }
		src := filepath.Join(exeDir(), name)
		if _, err := os.Stat(src); err != nil { continue }
		if err := os.Rename(src, dst); err != nil {
			data, readErr := ioutil.ReadFile(src)
			if readErr != nil { log.Printf("[config] 旧文件迁移失败 %s: %v", name, readErr); continue }
			if writeErr := ioutil.WriteFile(dst, data, 0600); writeErr != nil {
				log.Printf("[config] 旧文件迁移失败 %s: %v", name, writeErr)
			}
		}
	}
	return nil
}

func loadProvidersUnsafe() {
	if err := ensureConfigDir(); err != nil {
		log.Printf("[config] 创建配置目录失败: %v", err)
		return
	}
	path := providerConfigPath()
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		providers = make(map[string]Provider)
		example := []byte("# LumaChat 自定义服务商配置\n# 在网页设置中添加服务商，或按格式编辑本文件。\n# Base URL 填 API 根地址，不要填 /chat/completions。\n\n")
		if writeErr := writeConfigAtomically(path, example, 0600, "config"); writeErr != nil {
			log.Printf("[config] 初始化配置文件失败: %v", writeErr)
			return
		}
		log.Printf("[config] 尚未配置服务商，请在网页设置中添加自定义服务商")
		return
	}
	if err != nil {
		log.Printf("[config] 读取失败: %v", err)
		return
	}

	newProviders := make(map[string]Provider)
	lines := strings.Split(string(data), "\n")
	var curr *Provider
	flush := func() {
		if curr == nil { return }
		curr.Name = strings.TrimSpace(curr.Name)
		curr.BaseURL = strings.TrimRight(strings.TrimSpace(curr.BaseURL), "/")
		curr.APIKey = strings.TrimSpace(curr.APIKey)
		seen := make(map[string]bool)
		models := make([]string, 0, len(curr.Models))
		for _, model := range curr.Models {
			model = strings.TrimSpace(model)
			if model != "" && !seen[model] { seen[model] = true; models = append(models, model) }
		}
		curr.Models = models
		if curr.Name == "" && curr.BaseURL == "" && curr.APIKey == "" && len(curr.Models) == 0 { curr = nil; return }
		if curr.Name == "" || curr.BaseURL == "" || len(curr.Models) == 0 ||
			(!strings.HasPrefix(curr.BaseURL, "http://") && !strings.HasPrefix(curr.BaseURL, "https://")) {
			log.Printf("[config] 跳过无效服务商配置（name=%q）", curr.Name)
			curr = nil
			return
		}
		if curr.CreatedAt.IsZero() { curr.CreatedAt = time.Now() }
		newProviders[curr.Name] = *curr
		curr = nil
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") { continue }
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") { flush(); curr = &Provider{}; continue }
		if curr == nil { continue }
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 { continue }
		k, v := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch k {
		case "name": curr.Name = v
		case "base_url": curr.BaseURL = v
		case "api_key": curr.APIKey = v
		case "models": curr.Models = append(curr.Models, strings.Split(v, ",")...)
		}
	}
	flush()
	// Empty provider lists are valid and must replace the previous in-memory configuration.
	providers = newProviders
	log.Printf("[config] 已加载 %d 个自定义服务商", len(newProviders))
}

func loadUsersUnsafe() {
	if err := ensureConfigDir(); err != nil {
		log.Printf("[user] 创建配置目录失败: %v", err)
		return
	}
	data, err := ioutil.ReadFile(userDataPath())
	if os.IsNotExist(err) { return }
	if err != nil { log.Printf("[user] 读取失败: %v", err); return }
	var loaded map[string]*User
	if err := json.Unmarshal(data, &loaded); err != nil {
		log.Printf("[config] 用户文件解析失败: %v", err)
		return
	}
	if loaded == nil { loaded = make(map[string]*User) }
	for _, u := range loaded {
		if u == nil { continue }
		if u.Sessions == nil { u.Sessions = make(map[string]*ChatSession) }
		for _, session := range u.Sessions {
			if session != nil && len(session.Messages) > 60 {
				session.Messages = append([]Message(nil), session.Messages[len(session.Messages)-60:]...)
			}
		}
		if _, ok := u.Sessions[u.ActiveSessID]; !ok {
			for id := range u.Sessions { u.ActiveSessID = id; break }
		}
		if u.Theme == "" { u.Theme = "light" }
	}
	users = loaded
	log.Printf("[config] 已加载 %d 个用户", len(loaded))
}

func saveConfigUnsafe() error {
	if err := ensureConfigDir(); err != nil { return err }
	var sb strings.Builder
	sb.WriteString("# LumaChat v" + version + " 自定义服务商配置\n")
	sb.WriteString("# 修改后约 2 秒内自动热加载\n\n")
	for _, p := range providers {
		sb.WriteString("[provider]\n")
		sb.WriteString("name=" + p.Name + "\n")
		sb.WriteString("base_url=" + p.BaseURL + "\n")
		sb.WriteString("api_key=" + p.APIKey + "\n")
		sb.WriteString("models=" + strings.Join(p.Models, ",") + "\n\n")
	}
	return writeConfigAtomically(providerConfigPath(), []byte(sb.String()), 0600, "config")
}

func saveUsersSync() {
	mu.Lock()
	data, err := json.MarshalIndent(users, "", "  ")
	mu.Unlock()
	if err != nil { log.Printf("[user] 序列化失败: %v", err); return }
	if err := ensureConfigDir(); err != nil { log.Printf("[user] 创建配置目录失败: %v", err); return }
	if err := writeConfigAtomically(userDataPath(), data, 0600, "user"); err != nil { log.Printf("[user] 保存失败: %v", err) }
}

func writeConfigAtomically(path string, data []byte, mode os.FileMode, label string) error {
	tmp := path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("[%s] 写入临时文件失败: %w", label, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows can't replace an existing destination. Preserve the previous file if replacement fails.
		backup := path + ".bak"
		_ = os.Remove(backup)
		if _, statErr := os.Stat(path); statErr == nil {
			if backupErr := os.Rename(path, backup); backupErr != nil {
				_ = os.Remove(tmp)
				return fmt.Errorf("[%s] 备份旧文件失败: %w", label, backupErr)
			}
			if retryErr := os.Rename(tmp, path); retryErr != nil {
				_ = os.Rename(backup, path)
				_ = os.Remove(tmp)
				return fmt.Errorf("[%s] 替换配置文件失败: %w", label, retryErr)
			}
			_ = os.Remove(backup)
			return nil
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("[%s] 替换配置文件失败: %w", label, err)
	}
	return nil
}

func saveUsersDebounced() {
	select { case saveCh <- struct{}{}: default: }
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
			fi, err := os.Stat(providerConfigPath())
			if err != nil { continue }
			if fi.ModTime().After(lastMod) {
				lastMod = fi.ModTime()
				mu.Lock()
				loadProvidersUnsafe()
				mu.Unlock()
			}
		}
	}
}
