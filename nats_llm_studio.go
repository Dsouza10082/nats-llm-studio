package nats_llm_studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

type LMStudioClient struct {
	BaseURL   string
	ModelsDir string      
	HTTP      *http.Client
}

type ModelInfo struct {
	ID        string `json:"id"`
	Publisher string `json:"publisher"`
}


func NewLMStudioClient(baseUrl string, modelsDir string) *LMStudioClient {
	httpClient := &http.Client{
		Timeout: 2 * time.Minute,
	}

	return &LMStudioClient{
		BaseURL:   strings.TrimRight(baseUrl, "/"),
		ModelsDir: strings.TrimRight(modelsDir, "/"),
		HTTP:      httpClient,
	}
}

func (c *LMStudioClient) PullModel(ctx context.Context, identifier string) ([]byte, error) {
	if identifier == "" {
		return nil, fmt.Errorf("identifier is empty")
	}

	cmd := exec.CommandContext(ctx, "lms", "get", identifier)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("failed to execute 'lms get %s': %w", identifier, err)
	}

	return out, nil
}

func (c *LMStudioClient) getModelInfo(ctx context.Context, modelID string) (*ModelInfo, int, error) {
	endpoint := fmt.Sprintf("%s/api/v0/models/%s", c.BaseURL, url.PathEscape(modelID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating request for getModelInfo: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("error calling LM Studio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, resp.StatusCode, fmt.Errorf("LM Studio returned %d: %s", resp.StatusCode, string(body))
	}

	var info ModelInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("error decoding model response: %w", err)
	}
	return &info, resp.StatusCode, nil
}

func (c *LMStudioClient) unloadModel(ctx context.Context, modelID string) {
	if modelID == "" {
		return
	}
	cmd := exec.CommandContext(ctx, "lms", "unload", modelID)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("warning: failed to unload model %s: %v | output: %s", modelID, err, string(out))
	}
}

func (c *LMStudioClient) DeleteModel(ctx context.Context, modelID string) (string, error) {
	if modelID == "" {
		return "", errors.New("modelID is empty")
	}

	c.unloadModel(ctx, modelID)

	info, _, err := c.getModelInfo(ctx, modelID)
	if err != nil {
		return "", err
	}

	publisher := info.Publisher
	if publisher == "" {
		if parts := strings.SplitN(info.ID, "/", 2); len(parts) == 2 {
			publisher = parts[0]
		} else {
			return "", fmt.Errorf("unable to determine publisher of model %s", info.ID)
		}
	}

	dir := filepath.Join(c.ModelsDir, publisher, info.ID)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return dir, fmt.Errorf("model directory not found: %s", dir)
		}
		return dir, fmt.Errorf("error checking model directory: %w", err)
	}

	if err := os.RemoveAll(dir); err != nil {
		return dir, fmt.Errorf("error removing model directory %s: %w", dir, err)
	}

	return dir, nil
}


func (c *LMStudioClient) ListModels(ctx context.Context) (json.RawMessage, int, error) {
	endpoint := c.BaseURL + "/api/v0/models"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating request: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("error calling LM Studio: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("error reading LM Studio response: %w", err)
	}

	return json.RawMessage(body), resp.StatusCode, nil
}

func (c *LMStudioClient) Chat(ctx context.Context, payload []byte) (json.RawMessage, int, error) {
	endpoint := c.BaseURL + "/api/v0/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("error creating chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("error calling LM Studio: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("error reading chat response: %w", err)
	}

	return json.RawMessage(body), resp.StatusCode, nil
}

type Server struct {
	client *LMStudioClient
	nc     *nats.Conn
}

type NATSResponse struct {
	OK    bool        `json:"ok"`
	Error string      `json:"error,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

type PullModelRequest struct {
	Identifier string `json:"identifier"` // ex: "meta-llama/Meta-Llama-3-8B-Instruct"
}

type DeleteModelRequest struct {
	ModelID string `json:"model_id"` // ex: "granite-3.0-2b-instruct"
}

func NewServer(client *LMStudioClient, nc *nats.Conn) *Server {
	return &Server{
		client: client,
		nc:     nc,
	}
}

func respondJSON(msg *nats.Msg, v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("error serializing NATS response: %v", err)
		_ = msg.Respond([]byte(`{"ok":false,"error":"internal error serializing response"}`))
		return
	}
	if err := msg.Respond(b); err != nil {
		log.Printf("error responding to NATS message: %v", err)
	}
}

func respondError(msg *nats.Msg, err error, extraData map[string]interface{}) {
	resp := NATSResponse{
		OK:    false,
		Error: err.Error(),
		Data:  extraData,
	}
	respondJSON(msg, resp)
}

func (s *Server) onListModels(msg *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, status, err := s.client.ListModels(ctx)
	if err != nil {
		respondError(msg, err, map[string]interface{}{
			"http_status": status,
		})
		return
	}

	resp := NATSResponse{
		OK: true,
		Data: map[string]interface{}{
			"http_status": status,
			"models":      data, // json.RawMessage -> embed direto
		},
	}
	respondJSON(msg, resp)
}

func (s *Server) OnPullModel(msg *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var req PullModelRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		respondError(msg, fmt.Errorf("invalid JSON in PullModel: %w", err), nil)
		return
	}
	if req.Identifier == "" {
		respondError(msg, errors.New("'identifier' is required"), nil)
		return
	}

	out, err := s.client.PullModel(ctx, req.Identifier)
	if err != nil {
		resp := NATSResponse{
			OK:    false,
			Error: err.Error(),
			Data: map[string]interface{}{
				"model":  req.Identifier,
				"output": string(out),
			},
		}
		respondJSON(msg, resp)
		return
	}

	resp := NATSResponse{
		OK: true,
		Data: map[string]interface{}{
			"model":  req.Identifier,
			"output": string(out),
		},
	}
	respondJSON(msg, resp)
}

func (s *Server) OnDeleteModel(msg *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var req DeleteModelRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		respondError(msg, fmt.Errorf("invalid JSON in DeleteModel: %w", err), nil)
		return
	}
	if req.ModelID == "" {
		respondError(msg, errors.New("'model_id' is required"), nil)
		return
	}

	dir, err := s.client.DeleteModel(ctx, req.ModelID)
	if err != nil {
		resp := NATSResponse{
			OK:    false,
			Error: err.Error(),
			Data: map[string]interface{}{
				"model_id": req.ModelID,
				"dir":      dir,
			},
		}
		respondJSON(msg, resp)
		return
	}

	resp := NATSResponse{
		OK: true,
		Data: map[string]interface{}{
			"model_id":    req.ModelID,
			"deleted_dir": dir,
		},
	}
	respondJSON(msg, resp)
}


func (s *Server) OnChatModel(msg *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if len(msg.Data) == 0 {
		respondError(msg, errors.New("payload vazio em ChatModel"), nil)
		return
	}

	var tmp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(msg.Data, &tmp); err != nil {
		respondError(msg, fmt.Errorf("invalid JSON in ChatModel: %w", err), nil)
		return
	}
	if tmp.Model == "" {
		respondError(msg, errors.New("'model' is required in ChatModel"), nil)
		return
	}

	data, status, err := s.client.Chat(ctx, msg.Data)
	if err != nil {
		respondError(msg, err, map[string]interface{}{
			"http_status": status,
		})
		return
	}

	resp := NATSResponse{
		OK: true,
		Data: map[string]interface{}{
			"http_status": status,
			"response":    data,
		},
	}
	respondJSON(msg, resp)
}


