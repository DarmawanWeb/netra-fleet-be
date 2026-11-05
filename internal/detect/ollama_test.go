package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var mallCategories = []string{"trash", "spill", "floor_damage", "fallen_person"}

func TestNormalise(t *testing.T) {
	cases := []struct {
		answer string
		want   Result
	}{
		{`{"category": "trash", "confidence": 0.91, "description": "A bottle near the bench."}`,
			Result{"trash", 0.91, "A bottle near the bench."}},
		{`Sure! {"category": "Floor Damage", "confidence": 87, "description": "Cracked tile."}`,
			Result{"floor_damage", 0.87, "Cracked tile."}},
		{`{"category": "litter", "confidence": "0.7", "description": "Litter on the floor."}`,
			Result{"trash", 0.7, "Litter on the floor."}},
		{`{"category": "none", "confidence": 0.95, "description": "Clean floor."}`, Result{"none", 0.95, "Clean floor."}},
		{`I see a puddle of water on the floor`, Result{"spill", 0.6, "I see a puddle of water on the floor"}},
		{`{"category": "cat", "confidence": -2}`, Result{"none", 0, `{"category": "cat", "confidence": -2}`}},
	}
	for _, c := range cases {
		if got := Normalise(c.answer, mallCategories); got != c.want {
			t.Errorf("%q:\n got %+v\nwant %+v", c.answer, got, c.want)
		}
	}
	long := strings.Repeat("x", 600)
	if got := Normalise(`{"category":"trash","description":"`+long+`"}`, mallCategories); len(got.Description) != 400 {
		t.Fatalf("description not cut: %d", len(got.Description))
	}
}

func testImage(t *testing.T, w, h int, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255}) // #nosec G115 -- reduced mod 256
		}
	}
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDownscale(t *testing.T) {
	big := testImage(t, 2000, 1000, func(b *bytes.Buffer, i image.Image) error { return png.Encode(b, i) })
	out, err := Downscale(big, 1024)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil || img.Bounds().Dx() != 1024 || img.Bounds().Dy() != 512 {
		t.Fatalf("downscaled to %v (%v)", img.Bounds(), err)
	}
	small := testImage(t, 300, 200, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) })
	if out, err := Downscale(small, 1024); err != nil {
		t.Fatal(err)
	} else if img, _ := jpeg.Decode(bytes.NewReader(out)); img.Bounds().Dx() != 300 {
		t.Fatalf("small image resized to %v", img.Bounds())
	}
	if _, err := Downscale([]byte("not an image"), 1024); err == nil {
		t.Fatal("garbage decoded")
	}
}

func TestOllamaClassify(t *testing.T) {
	var got map[string]any
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]any{"response": `{"category":"spill","confidence":0.8,"description":"Water near the escalator."}`})
	}))
	defer ollama.Close()
	photo := filepath.Join(t.TempDir(), "a.jpg")
	if err := os.WriteFile(photo, testImage(t, 64, 48, func(b *bytes.Buffer, i image.Image) error { return jpeg.Encode(b, i, nil) }), 0o600); err != nil {
		t.Fatal(err)
	}
	d := NewOllama(ollama.URL+"/", "qwen2.5vl:7b", "Indonesian", "substation", mallCategories, 1024, 5*time.Second, "1h", 1)
	res, err := d.Classify(context.Background(), photo, Result{Category: "trash", Confidence: 0.55})
	if err != nil || res != (Result{"spill", 0.8, "Water near the escalator."}) {
		t.Fatalf("classify: %+v %v", res, err)
	}
	prompt, _ := got["prompt"].(string)
	if got["model"] != "qwen2.5vl:7b" || got["format"] != "json" || got["stream"] != false ||
		!strings.Contains(prompt, "substation patrol robot") || !strings.Contains(prompt, `"trash" (55% sure)`) ||
		!strings.Contains(prompt, "in Indonesian") || !strings.Contains(prompt, "fallen_person, none") {
		t.Fatalf("request: %v", got)
	}
	if images, _ := got["images"].([]any); len(images) != 1 {
		t.Fatalf("images: %v", got["images"])
	}

	down := NewOllama("http://127.0.0.1:1", "m", "English", "mall", mallCategories, 1024, time.Second, "1h", 1)
	if _, err := down.Classify(context.Background(), photo, Result{}); err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("unreachable model: %v", err)
	}
	if _, err := d.Classify(context.Background(), filepath.Join(t.TempDir(), "missing.jpg"), Result{}); err == nil {
		t.Fatal("missing photo accepted")
	}
}
