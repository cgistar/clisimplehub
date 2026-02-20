package codexplugin

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Codex CLI User-Agent patterns (aligned with claude-relay-service L269)
var codexCliPattern = regexp.MustCompile(`^(codex_vscode|codex_cli_rs|codex_exec)/[\d.]+`)

// Fixed Codex CLI instructions (aligned with claude-relay-service L292)
const codexCLIInstructions = "You are Codex, based on GPT-5. You are running as a coding agent in the Codex CLI on a user's computer.\n\n" +
	"## General\n\n" +
	"- When searching for text or files, prefer using `rg` or `rg --files` respectively because `rg` is much faster than alternatives like `grep`. (If the `rg` command is not found, then use alternatives.)\n\n" +
	"## Editing constraints\n\n" +
	"- Default to ASCII when editing or creating files. Only introduce non-ASCII or other Unicode characters when there is a clear justification and the file already uses them.\n" +
	"- Add succinct code comments that explain what is going on if code is not self-explanatory."

// isCodexCLI checks if the User-Agent indicates a Codex CLI request
func isCodexCLI(userAgent string) bool {
	return codexCliPattern.MatchString(userAgent)
}

// processRequestBody modifies the request body based on the request path and User-Agent.
// Aligned with claude-relay-service L336-340 (store field) and L273-298 (non-CLI adaptation).
func processRequestBody(body []byte, requestPath string, userAgent string) ([]byte, error) {
	isCompactRoute := isCompactResponsesPath(requestPath)
	isCodexCLIRequest := isCodexCLI(userAgent)
	shouldAdaptForNonCLI := strings.TrimSpace(userAgent) != "" && !isCodexCLIRequest

	// Parse request body
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		// If body is not valid JSON, return as-is
		return body, nil
	}

	// Handle store field based on route type
	if isCompactRoute {
		// Compact route: remove store field if present
		delete(reqBody, "store")
	} else {
		// Standard route: force set store=false
		reqBody["store"] = false
	}

	// If not Codex CLI request, apply adaptation (aligned with claude-relay-service L273-298)
	if shouldAdaptForNonCLI {
		// Remove incompatible fields
		fieldsToRemove := []string{
			"temperature",
			"top_p",
			"max_output_tokens",
			"user",
			"text_formatting",
			"truncation",
			"text",
			"service_tier",
			"prompt_cache_retention",
			"safety_identifier",
		}
		for _, field := range fieldsToRemove {
			delete(reqBody, field)
		}

		// Set fixed Codex CLI instructions
		reqBody["instructions"] = codexCLIInstructions
	}

	// Re-marshal
	return json.Marshal(reqBody)
}
