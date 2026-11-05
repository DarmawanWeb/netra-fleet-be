package detect

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // robots may upload PNG photos
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Ollama is the built-in final detection: the photo and the robot's early result go to a vision
// model on Ollama, which names the category actually present (or "none").
type Ollama struct {
	URL        string // Ollama base URL, e.g. http://localhost:11434
	Model      string
	Language   string // language of the description
	Site       string // what the robot patrols: mall, substation
	Categories []string
	MaxSide    int
	KeepAlive  string
	client     *http.Client
	gate       chan struct{}
}

// NewOllama returns a detector calling Ollama with at most concurrency requests at once.
func NewOllama(url, model, language, site string, categories []string, maxSide int, timeout time.Duration,
	keepAlive string, concurrency int) *Ollama {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Ollama{URL: strings.TrimRight(url, "/"), Model: model, Language: language, Site: site,
		Categories: categories, MaxSide: maxSide, KeepAlive: keepAlive,
		client: &http.Client{Timeout: timeout}, gate: make(chan struct{}, concurrency)}
}

const prompt = `You are the final reviewer of a %s patrol robot. The robot flagged this photo as "%s" (%.0f%% sure). ` +
	`Look carefully at the scene. Decide which single category is really present: %s. Use "none" when the robot was wrong. ` +
	`Answer only JSON: {"category": "<one of %s>", "confidence": <0..1>, "description": "<one sentence in %s describing what and where>"}.`

// Prompt returns the review prompt for an early result.
func (o *Ollama) Prompt(early Result) string {
	options := strings.Join(append(append([]string{}, o.Categories...), "none"), ", ")
	return fmt.Sprintf(prompt, o.Site, strings.ReplaceAll(early.Category, "_", " "), early.Confidence*100, options,
		options, o.Language)
}

// Classify sends the photo to the model and normalises its answer.
func (o *Ollama) Classify(ctx context.Context, imagePath string, early Result) (Result, error) {
	raw, err := os.ReadFile(imagePath)
	if err != nil {
		return Result{}, err
	}
	encoded, err := Downscale(raw, o.MaxSide)
	if err != nil {
		return Result{}, fmt.Errorf("unreadable image: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"model": o.Model, "prompt": o.Prompt(early), "images": []string{base64.StdEncoding.EncodeToString(encoded)},
		"format": "json", "stream": false, "keep_alive": o.KeepAlive,
		"options": map[string]any{"temperature": 0.0, "num_predict": 160},
	})
	select {
	case o.gate <- struct{}{}:
		defer func() { <-o.gate }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("model unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("model unavailable: ollama %d", resp.StatusCode)
	}
	var reply struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return Result{}, err
	}
	return Normalise(reply.Response, o.Categories), nil
}

// keywords map free text to a category when the model does not name one.
var keywords = map[string][]string{
	"trash":         {"trash", "garbage", "litter", "rubbish", "bottle", "cup", "wrapper", "plastic bag", "tissue"},
	"spill":         {"spill", "liquid", "puddle", "wet floor", "water on", "stain"},
	"floor_damage":  {"crack", "broken tile", "damaged floor", "hole", "loose tile", "chipped"},
	"fallen_person": {"fallen", "lying on the floor", "person lying", "collapsed", "unconscious"},
}

func keywordCategory(text string, categories []string) string {
	low := strings.ToLower(text)
	for _, c := range categories {
		words, ok := keywords[c]
		if !ok {
			words = []string{strings.ReplaceAll(c, "_", " ")}
		}
		for _, w := range words {
			if strings.Contains(low, w) {
				return c
			}
		}
	}
	return "none"
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

// Normalise turns a model answer into a Result: a known category or "none", a confidence in
// [0, 1] (percentages divided by 100, 0.6 when missing), a description of at most 400 characters.
func Normalise(answer string, categories []string) Result {
	var data map[string]any
	if m := jsonObject.FindString(answer); m != "" {
		_ = json.Unmarshal([]byte(m), &data)
	}
	raw := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(fmt.Sprint(valueOr(data, "category", "")))), " ", "_")
	description := strings.TrimSpace(fmt.Sprint(valueOr(data, "description", answer)))
	if description == "" || description == "<nil>" {
		description = strings.TrimSpace(answer)
	}
	if r := []rune(description); len(r) > 400 {
		description = string(r[:400])
	}
	category := raw
	if raw != "none" && !contains(categories, raw) {
		category = keywordCategory(raw+" "+description, categories)
	}
	confidence := 0.6
	switch v := valueOr(data, "confidence", nil).(type) {
	case float64:
		confidence = v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
			confidence = f
		}
	}
	if confidence > 1 {
		confidence /= 100
	}
	confidence = min(1, max(0, confidence))
	return Result{Category: category, Confidence: float64(int(confidence*1000+0.5)) / 1000, Description: description}
}

func valueOr(m map[string]any, key string, fallback any) any {
	if v, ok := m[key]; ok && v != nil {
		return v
	}
	return fallback
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Downscale decodes a JPEG or PNG photo, shrinks it so its longest side is at most maxSide (by
// averaging the source pixels under each output pixel) and returns it as JPEG.
func Downscale(raw []byte, maxSide int) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("empty image")
	}
	scale := 1.0
	if longest := max(w, h); maxSide > 0 && longest > maxSide {
		scale = float64(maxSide) / float64(longest)
	}
	ow, oh := max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < oh; y++ {
		y0, y1 := b.Min.Y+y*h/oh, b.Min.Y+(y+1)*h/oh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < ow; x++ {
			x0, x1 := b.Min.X+x*w/ow, b.Min.X+(x+1)*w/ow
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			i := dst.PixOffset(x, y)
			// #nosec G115 -- averages of 16-bit channels shifted to 8 bits fit a byte
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), 255
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
