package main

import (
	"encoding/json"
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
	if _, err := os.Stat(path); os.IsNotExist(err) {
		providers["DeepSeek官方"] = Provider{
			Name: "DeepSeek官方", BaseURL: "https://api.deepseek.com",
			APIKey: "sk-your-key-here", Models: []string{"deepseek-chat", "deepseek-reasoner"},
			CreatedAt: time.Now(),
		}
		saveConfigUnsafe()
		return
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		log.Printf("[config] 读取失败: %v", err)
		return
	}
	newProviders := make(map[string]Provider)
	lines := strings.Split(string(data), "\n")
	var curr *Provider
	flush := func() {
		if curr != nil && curr.Name != "" {
			if curr.CreatedAt.IsZero() { curr.CreatedAt = time.Now() }
			newProviders[curr.Name] = *curr
		}
		curr = nil
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") { continue }
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			curr = &Provider{}
			continue
		}
		if curr == nil { continue }
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 { continue }
		k, v := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch k {
		case "name": curr.Name = v
		case "base_url": curr.BaseURL = v
		case "api_key": curr.APIKey = v
		case "models":
			for _, m := range strings.Split(v, ",") {
				if m = strings.TrimSpace(m); m != "" { curr.Models = append(curr.Models, m) }
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
		if _, ok := u.Sessions[u.ActiveSessID]; !ok {
			for id := range u.Sessions { u.ActiveSessID = id; break }
		}
		if u.Theme == "" { u.Theme = "light" }
	}
	users = loaded
	log.Printf("[config] 已加载 %d 个用户", len(loaded))
}

func saveConfigUnsafe() {
	if err := ensureConfigDir(); err != nil {
		log.Printf("[config] 创建配置目录失败: %v", err)
		return
	}
	var sb strings.Builder
	sb.WriteString("# LumaChat v" + version + " 服务商配置\n")
	sb.WriteString("# 修改后 2 秒内自动热加载\n\n")
	for _, p := range providers {
		sb.WriteString("[provider]\n")
		sb.WriteString("name=" + p.Name + "\n")
		sb.WriteString("base_url=" + p.BaseURL + "\n")
		sb.WriteString("api_key=" + p.APIKey + "\n")
		sb.WriteString("models=" + strings.Join(p.Models, ",") + "\n\n")
	}
	writeConfigAtomically(providerConfigPath(), []byte(sb.String()), 0600, "config")
}

func saveUsersSync() {
	mu.Lock()
	data, err := json.MarshalIndent(users, "", "  ")
	mu.Unlock()
	if err != nil { log.Printf("[user] 序列化失败: %v", err); return }
	if err := ensureConfigDir(); err != nil { log.Printf("[user] 创建配置目录失败: %v", err); return }
	writeConfigAtomically(userDataPath(), data, 0600, "user")
}

func writeConfigAtomically(path string, data []byte, mode os.FileMode, label string) {
	tmp := path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, mode); err != nil {
		log.Printf("[%s] 写入临时文件失败: %v", label, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows does not replace an existing destination with os.Rename.
		if removeErr := os.Remove(path); removeErr == nil || os.IsNotExist(removeErr) {
			retryErr := os.Rename(tmp, path)
			if retryErr == nil { return }
			err = retryErr
		}
		_ = os.Remove(tmp)
		log.Printf("[%s] 替换配置文件失败: %v", label, err)
	}
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
