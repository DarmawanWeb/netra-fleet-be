// Package detect runs the server-side final classification of robot detections.
package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

// Result is a final classification.
type Result struct {
	Category    string  `json:"category"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description"`
}

// Detector classifies an anomaly image given the robot's early result.
type Detector interface {
	Classify(ctx context.Context, imagePath string, early Result) (Result, error)
}

// PassThrough confirms the robot's early result (no server model configured).
type PassThrough struct{}

// Classify returns the early result unchanged.
func (PassThrough) Classify(_ context.Context, _ string, early Result) (Result, error) {
	return early, nil
}

// HTTP posts the image and early result to an inference service that answers with a Result.
type HTTP struct {
	URL    string
	client *http.Client
}

// NewHTTP returns a detector calling url (multipart fields "image" and "early").
func NewHTTP(url string) *HTTP {
	return &HTTP{URL: url, client: &http.Client{Timeout: 120 * time.Second}}
}

// Classify calls the inference service.
func (d *HTTP) Classify(ctx context.Context, imagePath string, early Result) (Result, error) {
	img, err := os.ReadFile(imagePath)
	if err != nil {
		return Result{}, err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	meta, _ := json.Marshal(early)
	_ = w.WriteField("early", string(meta))
	part, _ := w.CreateFormFile("image", "image.jpg")
	_, _ = part.Write(img)
	_ = w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, &body)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := d.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("detector %d", resp.StatusCode)
	}
	var r Result
	return r, json.NewDecoder(resp.Body).Decode(&r)
}
