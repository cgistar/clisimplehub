package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const openAIImagesMainModel = "gpt-5.4-mini"

var dataTag = []byte("data:")

type imagePreparedRequest struct {
	Body           []byte
	ResponseFormat string
	StreamPrefix   string
}

type imageCallResult struct {
	Result        string
	RevisedPrompt string
	OutputFormat  string
	Size          string
	Background    string
	Quality       string
}

func PrepareOpenAIImageBody(raw []byte) ([]byte, error) {
	// Request path dispatch happens in PrepareOpenAIImageRequest. This fallback keeps
	// backend.Prepare safe when called with an already-converted image body.
	if gjson.GetBytes(raw, "tool_choice.type").String() == "image_generation" {
		out := raw
		out, _ = sjson.SetBytes(out, "model", openAIImagesMainModel)
		out, _ = sjson.SetBytes(out, "stream", true)
		out = deleteUnsupportedFields(out)
		return normalizeInstructions(out), nil
	}
	return raw, nil
}

func PrepareOpenAIImageRequest(path string, body []byte, model string, headers http.Header) ([]byte, string, string, error) {
	prepared, err := prepareOpenAIImageRequest(path, body, model, headers)
	if err != nil {
		return nil, "", "", err
	}
	out := prepared.Body
	out, _ = sjson.SetBytes(out, "model", openAIImagesMainModel)
	out, _ = sjson.SetBytes(out, "stream", true)
	out = deleteUnsupportedFields(out)
	out = normalizeInstructions(out)
	return out, prepared.ResponseFormat, prepared.StreamPrefix, nil
}

func BuildOpenAIImageResponse(data []byte, req Request, prepared *imagePreparedRequest) ([]byte, error) {
	responseFormat, _ := imageResponseOptions(req, prepared)
	outputItemsByIndex := make(map[int64][]byte)
	var outputItemsFallback [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		eventData := bytes.TrimSpace(line[len(dataTag):])
		switch gjson.GetBytes(eventData, "type").String() {
		case "response.output_item.done":
			collectOutputItemDone(eventData, outputItemsByIndex, &outputItemsFallback)
		case "response.completed":
			completedData := patchCompletedOutput(eventData, outputItemsByIndex, outputItemsFallback)
			results, createdAt, usageRaw, firstMeta, err := extractImagesFromResponsesCompleted(completedData)
			if err != nil {
				return nil, err
			}
			if len(results) == 0 {
				return nil, StatusError{Code: http.StatusBadGateway, Body: []byte(`{"error":{"message":"upstream did not return image output"}}`)}
			}
			return buildImagesAPIResponse(results, createdAt, usageRaw, firstMeta, responseFormat)
		}
	}
	return nil, StatusError{Code: http.StatusGatewayTimeout, Body: []byte(`{"error":{"message":"stream error: stream disconnected before completion"}}`)}
}

func BuildOpenAIImageStreamResponse(data []byte, req Request, prepared *imagePreparedRequest) ([]byte, error) {
	responseFormat, streamPrefix := imageResponseOptions(req, prepared)
	outputItemsByIndex := make(map[int64][]byte)
	var outputItemsFallback [][]byte
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		eventData := bytes.TrimSpace(line[len(dataTag):])
		switch gjson.GetBytes(eventData, "type").String() {
		case "response.output_item.done":
			collectOutputItemDone(eventData, outputItemsByIndex, &outputItemsFallback)
		case "response.image_generation_call.partial_image":
			if frame := buildImagePartialFrame(eventData, responseFormat, streamPrefix); len(frame) > 0 {
				out.Write(frame)
			}
		case "response.completed":
			completedData := patchCompletedOutput(eventData, outputItemsByIndex, outputItemsFallback)
			results, _, usageRaw, _, err := extractImagesFromResponsesCompleted(completedData)
			if err != nil {
				return nil, err
			}
			if len(results) == 0 {
				return nil, StatusError{Code: http.StatusBadGateway, Body: []byte(`{"error":{"message":"upstream did not return image output"}}`)}
			}
			for _, img := range results {
				if frame := buildImageCompletedFrame(img, usageRaw, responseFormat, streamPrefix); len(frame) > 0 {
					out.Write(frame)
				}
			}
			return out.Bytes(), nil
		}
	}
	return nil, StatusError{Code: http.StatusGatewayTimeout, Body: []byte(`{"error":{"message":"stream error: stream disconnected before completion"}}`)}
}

func BuildOpenAIImageStream(ctx context.Context, src io.ReadCloser, req Request, prepared *imagePreparedRequest) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer src.Close()
		if err := writeOpenAIImageStream(ctx, writer, src, req, prepared); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()
	return &imageStreamReadCloser{PipeReader: reader, source: src}
}

type imageStreamReadCloser struct {
	*io.PipeReader
	source io.Closer
}

func (r *imageStreamReadCloser) Close() error {
	readErr := r.PipeReader.Close()
	sourceErr := r.source.Close()
	if readErr != nil {
		return readErr
	}
	return sourceErr
}

func writeOpenAIImageStream(ctx context.Context, writer *io.PipeWriter, src io.Reader, req Request, prepared *imagePreparedRequest) error {
	responseFormat, streamPrefix := imageResponseOptions(req, prepared)
	outputItemsByIndex := make(map[int64][]byte)
	var outputItemsFallback [][]byte
	scanner := bufio.NewScanner(src)
	scanner.Buffer(nil, 52_428_800)
	writeFrame := func(frame []byte) error {
		if len(frame) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, err := writer.Write(frame)
		return err
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, dataTag) {
			continue
		}
		eventData := bytes.TrimSpace(line[len(dataTag):])
		switch gjson.GetBytes(eventData, "type").String() {
		case "response.output_item.done":
			collectOutputItemDone(eventData, outputItemsByIndex, &outputItemsFallback)
		case "response.image_generation_call.partial_image":
			if err := writeFrame(buildImagePartialFrame(eventData, responseFormat, streamPrefix)); err != nil {
				return err
			}
		case "response.completed":
			completedData := patchCompletedOutput(eventData, outputItemsByIndex, outputItemsFallback)
			results, _, usageRaw, _, err := extractImagesFromResponsesCompleted(completedData)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				return StatusError{Code: http.StatusBadGateway, Body: []byte(`{"error":{"message":"upstream did not return image output"}}`)}
			}
			for _, img := range results {
				if err := writeFrame(buildImageCompletedFrame(img, usageRaw, responseFormat, streamPrefix)); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return StatusError{Code: http.StatusGatewayTimeout, Body: []byte(`{"error":{"message":"stream error: stream disconnected before completion"}}`)}
}

func prepareOpenAIImageRequest(path string, rawJSON []byte, routeModel string, headers http.Header) (imagePreparedRequest, error) {
	if IsImagesGenerationsPath(path) {
		return prepareOpenAIImageGenerationJSON(rawJSON, routeModel)
	}
	if !IsImagesEditsPath(path) {
		return imagePreparedRequest{}, fmt.Errorf("unsupported OpenAI image endpoint path %q", path)
	}
	contentType := strings.TrimSpace(headers.Get("Content-Type"))
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return prepareOpenAIImageEditMultipart(rawJSON, routeModel, contentType)
	}
	return prepareOpenAIImageEditJSON(rawJSON, routeModel)
}

func defaultImagePreparedRequest(req Request) *imagePreparedRequest {
	_, streamPrefix := imageResponseOptions(req, nil)
	return &imagePreparedRequest{
		ResponseFormat: imageResponseFormatFromRequest(req.Body, req.Headers),
		StreamPrefix:   streamPrefix,
	}
}

func imageResponseOptions(req Request, prepared *imagePreparedRequest) (responseFormat string, streamPrefix string) {
	responseFormat = ""
	streamPrefix = ""
	if prepared != nil {
		responseFormat = strings.TrimSpace(prepared.ResponseFormat)
		streamPrefix = strings.TrimSpace(prepared.StreamPrefix)
	}
	if responseFormat == "" {
		responseFormat = imageResponseFormatFromRequest(req.Body, req.Headers)
	}
	if streamPrefix == "" {
		streamPrefix = "image_generation"
		if IsImagesEditsPath(req.Path) {
			streamPrefix = "image_edit"
		}
	}
	return normalizeImageResponseFormat(responseFormat), streamPrefix
}

func prepareOpenAIImageGenerationJSON(rawJSON []byte, routeModel string) (imagePreparedRequest, error) {
	if !json.Valid(rawJSON) {
		return imagePreparedRequest{}, invalidJSONError("generation")
	}
	prompt := strings.TrimSpace(gjson.GetBytes(rawJSON, "prompt").String())
	tool := buildOpenAIImageTool(rawJSON, routeModel, "generate", []string{"size", "quality", "background", "output_format", "moderation"}, []string{"output_compression", "partial_images"})
	body := buildImagesResponsesRequest(prompt, nil, tool)
	return imagePreparedRequest{Body: body, ResponseFormat: imageResponseFormatFromJSON(rawJSON), StreamPrefix: "image_generation"}, nil
}

func prepareOpenAIImageEditJSON(rawJSON []byte, routeModel string) (imagePreparedRequest, error) {
	if !json.Valid(rawJSON) {
		return imagePreparedRequest{}, invalidJSONError("edit")
	}
	prompt := strings.TrimSpace(gjson.GetBytes(rawJSON, "prompt").String())
	images := make([]string, 0)
	if imagesResult := gjson.GetBytes(rawJSON, "images"); imagesResult.IsArray() {
		for _, img := range imagesResult.Array() {
			if url := strings.TrimSpace(img.Get("image_url").String()); url != "" {
				images = append(images, url)
			}
		}
	}
	if imageURL := strings.TrimSpace(gjson.GetBytes(rawJSON, "image_url").String()); imageURL != "" {
		images = append(images, imageURL)
	}
	tool := buildOpenAIImageTool(rawJSON, routeModel, "edit", []string{"size", "quality", "background", "output_format", "input_fidelity", "moderation"}, []string{"output_compression", "partial_images"})
	if mask := strings.TrimSpace(gjson.GetBytes(rawJSON, "mask.image_url").String()); mask != "" {
		tool, _ = sjson.SetBytes(tool, "input_image_mask.image_url", mask)
	}
	body := buildImagesResponsesRequest(prompt, images, tool)
	return imagePreparedRequest{Body: body, ResponseFormat: imageResponseFormatFromJSON(rawJSON), StreamPrefix: "image_edit"}, nil
}

func prepareOpenAIImageEditMultipart(rawBody []byte, routeModel string, contentType string) (imagePreparedRequest, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return imagePreparedRequest{}, fmt.Errorf("parse multipart content type failed: %w", err)
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return imagePreparedRequest{}, fmt.Errorf("multipart boundary is required")
	}
	reader := multipart.NewReader(bytes.NewReader(rawBody), boundary)
	form, err := reader.ReadForm(32 << 20)
	if err != nil {
		return imagePreparedRequest{}, fmt.Errorf("parse multipart form failed: %w", err)
	}
	defer form.RemoveAll()

	prompt := strings.TrimSpace(formValue(form, "prompt"))
	responseFormat := normalizeImageResponseFormat(formValue(form, "response_format"))
	tool := []byte(`{"type":"image_generation","action":"edit"}`)
	tool, _ = sjson.SetBytes(tool, "model", imageToolModel(formValue(form, "model"), routeModel))
	for _, field := range []string{"size", "quality", "background", "output_format", "input_fidelity", "moderation"} {
		if value := strings.TrimSpace(formValue(form, field)); value != "" {
			tool, _ = sjson.SetBytes(tool, field, value)
		}
	}
	for _, field := range []string{"output_compression", "partial_images"} {
		if value := strings.TrimSpace(formValue(form, field)); value != "" {
			if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
				tool, _ = sjson.SetBytes(tool, field, parsed)
			}
		}
	}

	images := make([]string, 0)
	for _, fh := range multipartImageFiles(form) {
		dataURL, err := multipartFileToDataURL(fh)
		if err != nil {
			return imagePreparedRequest{}, err
		}
		images = append(images, dataURL)
	}
	if maskFiles := form.File["mask"]; len(maskFiles) > 0 && maskFiles[0] != nil {
		dataURL, err := multipartFileToDataURL(maskFiles[0])
		if err != nil {
			return imagePreparedRequest{}, err
		}
		tool, _ = sjson.SetBytes(tool, "input_image_mask.image_url", dataURL)
	}
	body := buildImagesResponsesRequest(prompt, images, tool)
	return imagePreparedRequest{Body: body, ResponseFormat: responseFormat, StreamPrefix: "image_edit"}, nil
}

func imageResponseFormatFromRequest(body []byte, headers http.Header) string {
	contentType := strings.TrimSpace(headers.Get("Content-Type"))
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		// Multipart response_format has already been folded into the request body;
		// OpenAI defaults to b64_json when the caller does not specify it.
		return "b64_json"
	}
	return imageResponseFormatFromJSON(body)
}

func imageResponseFormatFromJSON(rawJSON []byte) string {
	return normalizeImageResponseFormat(gjson.GetBytes(rawJSON, "response_format").String())
}

func normalizeImageResponseFormat(responseFormat string) string {
	if strings.EqualFold(strings.TrimSpace(responseFormat), "url") {
		return "url"
	}
	return "b64_json"
}

func imageToolModel(requestModel string, routeModel string) string {
	model := strings.TrimSpace(requestModel)
	if model == "" {
		model = strings.TrimSpace(routeModel)
	}
	if model == "" {
		model = defaultImageToolModel
	}
	return model
}

func buildOpenAIImageTool(rawJSON []byte, routeModel string, action string, stringFields []string, numberFields []string) []byte {
	tool := []byte(`{"type":"image_generation","action":""}`)
	tool, _ = sjson.SetBytes(tool, "action", action)
	tool, _ = sjson.SetBytes(tool, "model", imageToolModel(gjson.GetBytes(rawJSON, "model").String(), routeModel))
	for _, field := range stringFields {
		if value := strings.TrimSpace(gjson.GetBytes(rawJSON, field).String()); value != "" {
			tool, _ = sjson.SetBytes(tool, field, value)
		}
	}
	for _, field := range numberFields {
		if value := gjson.GetBytes(rawJSON, field); value.Exists() && value.Type == gjson.Number {
			tool, _ = sjson.SetBytes(tool, field, value.Int())
		}
	}
	return tool
}

func buildImagesResponsesRequest(prompt string, images []string, toolJSON []byte) []byte {
	req := []byte(`{"instructions":"","stream":true,"reasoning":{"effort":"medium","summary":"auto"},"parallel_tool_calls":true,"include":["reasoning.encrypted_content"],"model":"","store":false,"tool_choice":{"type":"image_generation"}}`)
	req, _ = sjson.SetBytes(req, "model", openAIImagesMainModel)
	input := []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}]`)
	input, _ = sjson.SetBytes(input, "0.content.0.text", prompt)
	contentIndex := 1
	for _, img := range images {
		if strings.TrimSpace(img) == "" {
			continue
		}
		part := []byte(`{"type":"input_image","image_url":""}`)
		part, _ = sjson.SetBytes(part, "image_url", img)
		input, _ = sjson.SetRawBytes(input, fmt.Sprintf("0.content.%d", contentIndex), part)
		contentIndex++
	}
	req, _ = sjson.SetRawBytes(req, "input", input)
	req, _ = sjson.SetRawBytes(req, "tools", []byte(`[]`))
	if len(toolJSON) > 0 && json.Valid(toolJSON) {
		req, _ = sjson.SetRawBytes(req, "tools.-1", toolJSON)
	}
	return req
}

func formValue(form *multipart.Form, key string) string {
	if form == nil || len(form.Value[key]) == 0 {
		return ""
	}
	return strings.TrimSpace(form.Value[key][0])
}

func multipartImageFiles(form *multipart.Form) []*multipart.FileHeader {
	if form == nil {
		return nil
	}
	if files := form.File["image[]"]; len(files) > 0 {
		return files
	}
	return form.File["image"]
}

func multipartFileToDataURL(fileHeader *multipart.FileHeader) (string, error) {
	if fileHeader == nil {
		return "", fmt.Errorf("upload file is nil")
	}
	f, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("open upload file failed: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read upload file failed: %w", err)
	}
	mediaType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func collectOutputItemDone(eventData []byte, outputItemsByIndex map[int64][]byte, outputItemsFallback *[][]byte) {
	itemResult := gjson.GetBytes(eventData, "item")
	if !itemResult.Exists() || itemResult.Type != gjson.JSON {
		return
	}
	outputIndexResult := gjson.GetBytes(eventData, "output_index")
	if outputIndexResult.Exists() {
		outputItemsByIndex[outputIndexResult.Int()] = []byte(itemResult.Raw)
		return
	}
	*outputItemsFallback = append(*outputItemsFallback, []byte(itemResult.Raw))
}

func patchCompletedOutput(eventData []byte, outputItemsByIndex map[int64][]byte, outputItemsFallback [][]byte) []byte {
	outputResult := gjson.GetBytes(eventData, "response.output")
	if outputResult.Exists() && outputResult.IsArray() && len(outputResult.Array()) > 0 {
		return eventData
	}
	if len(outputItemsByIndex) == 0 && len(outputItemsFallback) == 0 {
		return eventData
	}
	var items [][]byte
	for idx := int64(0); ; idx++ {
		item, ok := outputItemsByIndex[idx]
		if !ok {
			break
		}
		items = append(items, item)
	}
	items = append(items, outputItemsFallback...)
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(item)
	}
	buf.WriteByte(']')
	out, _ := sjson.SetRawBytes(eventData, "response.output", buf.Bytes())
	return out
}

func extractImagesFromResponsesCompleted(payload []byte) (results []imageCallResult, createdAt int64, usageRaw []byte, firstMeta imageCallResult, err error) {
	if gjson.GetBytes(payload, "type").String() != "response.completed" {
		return nil, 0, nil, imageCallResult{}, fmt.Errorf("unexpected event type")
	}
	createdAt = gjson.GetBytes(payload, "response.created_at").Int()
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	if output := gjson.GetBytes(payload, "response.output"); output.IsArray() {
		for _, item := range output.Array() {
			if item.Get("type").String() != "image_generation_call" {
				continue
			}
			res := strings.TrimSpace(item.Get("result").String())
			if res == "" {
				continue
			}
			entry := imageCallResult{
				Result:        res,
				RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
				OutputFormat:  strings.TrimSpace(item.Get("output_format").String()),
				Size:          strings.TrimSpace(item.Get("size").String()),
				Background:    strings.TrimSpace(item.Get("background").String()),
				Quality:       strings.TrimSpace(item.Get("quality").String()),
			}
			if len(results) == 0 {
				firstMeta = entry
			}
			results = append(results, entry)
		}
	}
	if usage := gjson.GetBytes(payload, "response.tool_usage.image_gen"); usage.Exists() && usage.IsObject() {
		usageRaw = []byte(usage.Raw)
	}
	return results, createdAt, usageRaw, firstMeta, nil
}

func buildImagesAPIResponse(results []imageCallResult, createdAt int64, usageRaw []byte, firstMeta imageCallResult, responseFormat string) ([]byte, error) {
	out := []byte(`{"created":0,"data":[]}`)
	out, _ = sjson.SetBytes(out, "created", createdAt)
	responseFormat = normalizeImageResponseFormat(responseFormat)
	for _, img := range results {
		item := []byte(`{}`)
		if responseFormat == "url" {
			item, _ = sjson.SetBytes(item, "url", "data:"+mimeTypeFromOutputFormat(img.OutputFormat)+";base64,"+img.Result)
		} else {
			item, _ = sjson.SetBytes(item, "b64_json", img.Result)
		}
		if img.RevisedPrompt != "" {
			item, _ = sjson.SetBytes(item, "revised_prompt", img.RevisedPrompt)
		}
		out, _ = sjson.SetRawBytes(out, "data.-1", item)
	}
	if firstMeta.Background != "" {
		out, _ = sjson.SetBytes(out, "background", firstMeta.Background)
	}
	if firstMeta.OutputFormat != "" {
		out, _ = sjson.SetBytes(out, "output_format", firstMeta.OutputFormat)
	}
	if firstMeta.Quality != "" {
		out, _ = sjson.SetBytes(out, "quality", firstMeta.Quality)
	}
	if firstMeta.Size != "" {
		out, _ = sjson.SetBytes(out, "size", firstMeta.Size)
	}
	if len(usageRaw) > 0 && json.Valid(usageRaw) {
		out, _ = sjson.SetRawBytes(out, "usage", usageRaw)
	}
	return out, nil
}

func buildImagePartialFrame(payload []byte, responseFormat string, streamPrefix string) []byte {
	b64 := strings.TrimSpace(gjson.GetBytes(payload, "partial_image_b64").String())
	if b64 == "" {
		return nil
	}
	outputFormat := strings.TrimSpace(gjson.GetBytes(payload, "output_format").String())
	eventName := strings.TrimSpace(streamPrefix) + ".partial_image"
	data := []byte(`{"type":"","partial_image_index":0}`)
	data, _ = sjson.SetBytes(data, "type", eventName)
	data, _ = sjson.SetBytes(data, "partial_image_index", gjson.GetBytes(payload, "partial_image_index").Int())
	if normalizeImageResponseFormat(responseFormat) == "url" {
		data, _ = sjson.SetBytes(data, "url", "data:"+mimeTypeFromOutputFormat(outputFormat)+";base64,"+b64)
	} else {
		data, _ = sjson.SetBytes(data, "b64_json", b64)
	}
	return buildSSEFrame(eventName, data)
}

func buildImageCompletedFrame(img imageCallResult, usageRaw []byte, responseFormat string, streamPrefix string) []byte {
	eventName := strings.TrimSpace(streamPrefix) + ".completed"
	data := []byte(`{"type":""}`)
	data, _ = sjson.SetBytes(data, "type", eventName)
	if normalizeImageResponseFormat(responseFormat) == "url" {
		data, _ = sjson.SetBytes(data, "url", "data:"+mimeTypeFromOutputFormat(img.OutputFormat)+";base64,"+img.Result)
	} else {
		data, _ = sjson.SetBytes(data, "b64_json", img.Result)
	}
	if len(usageRaw) > 0 && json.Valid(usageRaw) {
		data, _ = sjson.SetRawBytes(data, "usage", usageRaw)
	}
	return buildSSEFrame(eventName, data)
}

func buildSSEFrame(eventName string, data []byte) []byte {
	var buf bytes.Buffer
	if strings.TrimSpace(eventName) != "" {
		buf.WriteString("event: ")
		buf.WriteString(eventName)
		buf.WriteString("\n")
	}
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")
	return buf.Bytes()
}

func mimeTypeFromOutputFormat(outputFormat string) string {
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
