package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type uploadContextResult struct {
	UploadContext string    `json:"upload_context"`
	ExpiresAt     time.Time `json:"expires_at"`
	UploadURL     string    `json:"upload_url"`
}

type prpBoundary interface {
	createUploadContext(accessToken string) (uploadContextResult, error)
}

type prpClient struct {
	baseURL string
	client  *http.Client
}

func (c *prpClient) createUploadContext(accessToken string) (uploadContextResult, error) {
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/upload-contexts", nil)
	if err != nil {
		return uploadContextResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := c.client.Do(request)
	if err != nil {
		return uploadContextResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return uploadContextResult{}, fmt.Errorf("PRP rejected upload context: HTTP %d", response.StatusCode)
	}
	var result uploadContextResult
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return uploadContextResult{}, fmt.Errorf("decode PRP upload context: %w", err)
	}
	parsed, err := url.Parse(result.UploadURL)
	if result.UploadContext == "" || !result.ExpiresAt.After(time.Now()) ||
		err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return uploadContextResult{}, fmt.Errorf("PRP upload context response is incomplete")
	}
	return result, nil
}
