// Package ai 是通道 B：CRM 自己调用大模型（OpenAI 兼容端点），
// 承担高频小任务——交互摘要、意向提取、跟进草稿。
// 批量重活（复盘、清洗）走通道 A 的自动化接口 + 外部 AI 工具。
package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config 来自后台设置页（settings 表），每次任务现读，改配置即时生效。
type Config struct {
	BaseURL string // 如 https://api.deepseek.com/v1
	APIKey  string
	Model   string // 如 deepseek-chat
}

func (c Config) Ready() bool { return c.BaseURL != "" && c.Model != "" }

var httpClient = &http.Client{Timeout: 90 * time.Second}

// Chat 调 {base}/chat/completions，返回首个回复文本。
func Chat(cfg Config, system, user string) (string, error) {
	if !cfg.Ready() {
		return "", errors.New("AI 模型未配置（设置页填写端点与模型）")
	}
	reqBody, err := json.Marshal(map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.3,
	})
	if err != nil {
		return "", err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("模型响应解析失败（HTTP %d）: %s", resp.StatusCode, truncate(string(body), 200))
	}
	if out.Error.Message != "" {
		return "", fmt.Errorf("模型返回错误: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("模型无回复（HTTP %d）: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return out.Choices[0].Message.Content, nil
}

// StripJSON 剥掉模型偶尔包裹的 ```json 围栏，返回可解析的 JSON 文本。
func StripJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}
	// 容错：截取首个 { 到最后一个 }
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
