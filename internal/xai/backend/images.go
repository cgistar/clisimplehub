package backend

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	DefaultXAIImageModel = "grok-imagine-image"
	XAIImageQualityModel = "grok-imagine-image-quality"
)

func IsXAIImageModel(model string) bool {
	prefix, base := modelParts(model)
	base = strings.ToLower(strings.TrimSpace(base))
	if base != DefaultXAIImageModel && base != XAIImageQualityModel {
		return false
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	return prefix == "" || prefix == "xai" || prefix == "x-ai" || prefix == "grok"
}

func modelParts(model string) (string, string) {
	model = strings.TrimSpace(model)
	if i := strings.LastIndex(model, "/"); i >= 0 && i < len(model)-1 {
		return strings.TrimSpace(model[:i]), strings.TrimSpace(model[i+1:])
	}
	return "", model
}

func NormalizeImageResponseFormat(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "url") {
		return "url"
	}
	return "b64_json"
}

func canonicalImageModel(model string) string {
	_, base := modelParts(model)
	if strings.EqualFold(base, XAIImageQualityModel) {
		return XAIImageQualityModel
	}
	return DefaultXAIImageModel
}

func imageAspectRatio(raw, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1:1", "square":
		return "1:1"
	case "16:9", "landscape":
		return "16:9"
	case "9:16", "portrait":
		return "9:16"
	case "4:3":
		return "4:3"
	case "3:4":
		return "3:4"
	case "3:2":
		return "3:2"
	case "2:3":
		return "2:3"
	default:
		return fallback
	}
}

func imageAspectRatioFromSize(size, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1024x1024", "2048x2048", "1:1":
		return "1:1"
	case "1792x1024", "16:9":
		return "16:9"
	case "1024x1792", "9:16":
		return "9:16"
	case "1536x1024", "3:2":
		return "3:2"
	case "1024x1536", "2:3":
		return "2:3"
	default:
		return fallback
	}
}

func imageResolution(raw, size, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1k", "2k":
		return strings.ToLower(strings.TrimSpace(raw))
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(size)), "2048") {
		return "2k"
	}
	return fallback
}

func imageRef(url string) []byte {
	out := []byte(`{"type":"image_url","url":""}`)
	out, _ = sjson.SetBytes(out, "url", strings.TrimSpace(url))
	return out
}

func buildImageBase(model, prompt, format, aspect, resolution string, n int64) []byte {
	out := []byte(`{}`)
	out, _ = sjson.SetBytes(out, "model", canonicalImageModel(model))
	out, _ = sjson.SetBytes(out, "prompt", strings.TrimSpace(prompt))
	out, _ = sjson.SetBytes(out, "response_format", NormalizeImageResponseFormat(format))
	if aspect != "" {
		out, _ = sjson.SetBytes(out, "aspect_ratio", aspect)
	}
	if resolution != "" {
		out, _ = sjson.SetBytes(out, "resolution", resolution)
	}
	if n > 0 {
		out, _ = sjson.SetBytes(out, "n", n)
	}
	return out
}

func BuildXAIImageGeneration(raw []byte, model, format string) []byte {
	size := strings.TrimSpace(gjson.GetBytes(raw, "size").String())
	aspect := imageAspectRatio(gjson.GetBytes(raw, "aspect_ratio").String(), "")
	aspect = imageAspectRatioFromSize(size, aspect)
	if aspect == "" {
		aspect = "1:1"
	}
	resolution := imageResolution(gjson.GetBytes(raw, "resolution").String(), size, "1k")
	var n int64
	if v := gjson.GetBytes(raw, "n"); v.Exists() && v.Type == gjson.Number {
		n = v.Int()
	}
	return buildImageBase(model, gjson.GetBytes(raw, "prompt").String(), format, aspect, resolution, n)
}

func CollectXAIImages(raw []byte) []string {
	var out []string
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	read := func(v gjson.Result) {
		if v.Type == gjson.String {
			add(v.String())
			return
		}
		add(v.Get("image_url.url").String())
		if x := v.Get("image_url"); x.Type == gjson.String {
			add(x.String())
		}
		add(v.Get("url").String())
	}
	if v := gjson.GetBytes(raw, "image"); v.Exists() {
		read(v)
	}
	if a := gjson.GetBytes(raw, "images"); a.IsArray() {
		for _, v := range a.Array() {
			read(v)
		}
	}
	return out
}

func BuildXAIImageEdit(raw []byte, model, format string, images []string) []byte {
	size := strings.TrimSpace(gjson.GetBytes(raw, "size").String())
	aspect := imageAspectRatio(gjson.GetBytes(raw, "aspect_ratio").String(), "")
	aspect = imageAspectRatioFromSize(size, aspect)
	resolution := imageResolution(gjson.GetBytes(raw, "resolution").String(), size, "")
	var n int64
	if v := gjson.GetBytes(raw, "n"); v.Exists() && v.Type == gjson.Number {
		n = v.Int()
	}
	out := buildImageBase(model, gjson.GetBytes(raw, "prompt").String(), format, aspect, resolution, n)
	if len(images) == 1 {
		out, _ = sjson.SetRawBytes(out, "image", imageRef(images[0]))
		return out
	}
	for _, img := range images {
		out, _ = sjson.SetRawBytes(out, "images.-1", imageRef(img))
	}
	return out
}

func BuildOpenAIImageResponse(payload []byte, format string) ([]byte, error) {
	if !json.Valid(payload) {
		return nil, fmt.Errorf("upstream returned invalid image response JSON")
	}
	created := gjson.GetBytes(payload, "created").Int()
	if created <= 0 {
		created = time.Now().Unix()
	}
	data := gjson.GetBytes(payload, "data")
	if !data.IsArray() {
		return nil, fmt.Errorf("upstream did not return image output")
	}
	out := []byte(`{"created":0,"data":[]}`)
	out, _ = sjson.SetBytes(out, "created", created)
	count := 0
	format = NormalizeImageResponseFormat(format)
	for _, src := range data.Array() {
		b64, url := strings.TrimSpace(src.Get("b64_json").String()), strings.TrimSpace(src.Get("url").String())
		if b64 == "" && url == "" {
			continue
		}
		item := []byte(`{}`)
		if format == "url" {
			if url != "" {
				item, _ = sjson.SetBytes(item, "url", url)
			} else {
				item, _ = sjson.SetBytes(item, "url", "data:image/png;base64,"+b64)
			}
		} else if b64 != "" {
			item, _ = sjson.SetBytes(item, "b64_json", b64)
		} else {
			item, _ = sjson.SetBytes(item, "url", url)
		}
		if p := strings.TrimSpace(src.Get("revised_prompt").String()); p != "" {
			item, _ = sjson.SetBytes(item, "revised_prompt", p)
		}
		out, _ = sjson.SetRawBytes(out, "data.-1", item)
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("upstream did not return image output")
	}
	if u := gjson.GetBytes(payload, "usage"); u.IsObject() {
		out, _ = sjson.SetRawBytes(out, "usage", []byte(u.Raw))
	}
	return out, nil
}
