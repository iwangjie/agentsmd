// Package engine asks a local model gateway to rewrite a prompt file. The
// gateway speaks the OpenAI chat API, so one client covers every model it
// fronts; the app holds no credentials beyond the gateway's own key.
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultBaseURL is the local gateway the app talks to.
const DefaultBaseURL = "http://127.0.0.1:3425/v1"

// Gateway is the model API the app borrows.
type Gateway struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// Model is one entry of the gateway's catalog.
type Model struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	ContextLen int    `json:"contextLength"`
	// ChatNative is true when the gateway lists the OpenAI chat protocol
	// among the model's own endpoints.
	ChatNative bool `json:"chatNative"`
	// Native lists the protocols the gateway serves the model with.
	Native []string `json:"native"`
}

// Status is what the app knows about the gateway: reachable, which models it
// offers, and which one is chosen.
type Status struct {
	Reachable bool    `json:"reachable"`
	BaseURL   string  `json:"baseURL"`
	KeySet    bool    `json:"keySet"`
	Models    []Model `json:"models"`
	Model     string  `json:"model"`
	Detail    string  `json:"detail"`
}

// New reads the gateway's address and key from the environment. There is no
// built-in key: a gateway without one accepts requests as they are, and one
// that needs a key gets it from MAGPIE_API_KEY.
func New(stateDir string) *Gateway {
	g := &Gateway{
		BaseURL: firstNonEmpty(os.Getenv("MAGPIE_BASE_URL"), DefaultBaseURL),
		APIKey:  os.Getenv("MAGPIE_API_KEY"),
		Model:   os.Getenv("MAGPIE_MODEL"),
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
	}
	if g.Model == "" {
		g.Model = readSavedModel(stateDir)
	}
	return g
}

// SaveModel remembers the chosen model beside the app's other state.
func SaveModel(stateDir, id string) {
	if stateDir == "" {
		return
	}
	_ = os.MkdirAll(stateDir, 0o755)
	_ = os.WriteFile(filepath.Join(stateDir, "model"), []byte(id), 0o644)
}

func readSavedModel(stateDir string) string {
	data, err := os.ReadFile(filepath.Join(stateDir, "model"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Discover lists the gateway's models. A model that serves only a native
// endpoint still answers the chat API, so the catalog is offered whole.
func (g *Gateway) Discover(ctx context.Context) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url("/models"), nil)
	if err != nil {
		return nil, err
	}
	g.authorize(req)

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("模型服务返回 HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID          string   `json:"id"`
			DisplayName string   `json:"display_name"`
			OwnedBy     string   `json:"owned_by"`
			Context     int      `json:"context_length"`
			Endpoints   []string `json:"native_endpoints"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	models := make([]Model, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID == "" {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		native := m.Endpoints
		chat := false
		for _, ep := range native {
			if ep == "/v1/chat/completions" {
				chat = true
			}
		}
		models = append(models, Model{
			ID:         m.ID,
			Name:       name,
			Provider:   m.OwnedBy,
			ContextLen: m.Context,
			ChatNative: chat,
			Native:     native,
		})
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].Name < models[j].Name
	})
	return models, nil
}

// Status reports the gateway and its catalog, without failing when it is
// down: the app still works offline, with local transforms only.
func (g *Gateway) Status(ctx context.Context) Status {
	st := Status{BaseURL: g.BaseURL, KeySet: g.APIKey != "", Model: g.Model}
	models, err := g.Discover(ctx)
	if err != nil {
		st.Detail = err.Error()
		return st
	}
	st.Reachable = true
	st.Models = models
	return st
}

// Choose sets the model, and saves the choice.
func (g *Gateway) Choose(stateDir, id string) {
	g.Model = id
	SaveModel(stateDir, id)
}

// ErrNoModel is returned when a rewrite is asked for without a model.
var ErrNoModel = errors.New("no model is selected")

// Chat sends one prompt and returns the answer's text.
func (g *Gateway) Chat(ctx context.Context, model, prompt string) (string, error) {
	if model == "" {
		return "", ErrNoModel
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0,
		"max_tokens":  8000,
		"stream":      false,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url("/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	g.authorize(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("模型服务返回 HTTP %d：%s", resp.StatusCode, truncate(string(data), 240))
	}

	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if payload.Error != nil && payload.Error.Message != "" {
		return "", errors.New(payload.Error.Message)
	}
	if len(payload.Choices) == 0 {
		return "", errors.New("模型没有返回内容")
	}
	out := strings.TrimSpace(payload.Choices[0].Message.Content)
	if out == "" {
		return "", errors.New("模型没有返回内容")
	}
	return out, nil
}

func (g *Gateway) url(path string) string {
	return strings.TrimRight(g.BaseURL, "/") + path
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// authorize sends the key only when there is one, so a gateway that does not
// ask for a key is not handed an empty bearer token.
func (g *Gateway) authorize(req *http.Request) {
	if g.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+g.APIKey)
	}
}
