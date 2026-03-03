package converters

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	defaultBudgetTokens     = 20000
	maxBudgetTokens         = 24576
	kiroCLIOrigin           = "KIRO_CLI"
	maxHistoryMessages      = 250
	maxCurrentWorkingDirLen = 256

	systemAssistantAck = "I will fully incorporate this information when generating my responses, and explicitly acknowledge relevant parts of the summary when answering questions."
)

func generateThinkingPrefix(thinking *AnthropicThinking, outputConfig *AnthropicOutputCfg) string {
	if thinking == nil {
		return ""
	}

	switch thinking.Type {
	case "enabled":
		budgetTokens := thinking.BudgetTokens
		if budgetTokens <= 0 {
			budgetTokens = defaultBudgetTokens
		}
		if budgetTokens > maxBudgetTokens {
			budgetTokens = maxBudgetTokens
		}
		return fmt.Sprintf(
			"<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>",
			budgetTokens,
		)
	case "adaptive":
		effort := "high"
		if outputConfig != nil && outputConfig.Effort != "" {
			effort = outputConfig.Effort
		}
		return fmt.Sprintf(
			"<thinking_mode>adaptive</thinking_mode><thinking_effort>%s</thinking_effort>",
			effort,
		)
	}

	return ""
}

func hasThinkingTags(content string) bool {
	return strings.Contains(content, "<thinking_mode>") || strings.Contains(content, "<max_thinking_length>")
}

func resolveOperatingSystem() string {
	switch runtime.GOOS {
	case "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}

func resolveCurrentWorkingDirectory() string {
	wd, err := os.Getwd()
	if err != nil {
		return "/"
	}
	wd = strings.TrimSpace(wd)
	if wd == "" {
		return "/"
	}
	if len(wd) > maxCurrentWorkingDirLen {
		return truncateUTF8Safe(wd, maxCurrentWorkingDirLen)
	}
	return wd
}

func buildEnvState() *KiroEnvState {
	return &KiroEnvState{
		OperatingSystem:         resolveOperatingSystem(),
		CurrentWorkingDirectory: resolveCurrentWorkingDirectory(),
	}
}

func formatCurrentTime(now time.Time) string {
	return now.Format("Monday, 2006-01-02T15:04:05.000Z07:00")
}

func buildContextOnlyContent(contextText string) string {
	return strings.Join([]string{
		"--- CONTEXT ENTRY BEGIN ---",
		contextText,
		"--- CONTEXT ENTRY END ---",
	}, "\n")
}

func buildPromptContent(promptContent string) string {
	return strings.Join([]string{
		"--- CONTEXT ENTRY BEGIN ---",
		fmt.Sprintf("Current time: %s", formatCurrentTime(time.Now())),
		"--- CONTEXT ENTRY END ---",
		"",
		"--- USER MESSAGE BEGIN ---",
		promptContent,
		"--- USER MESSAGE END ---",
	}, "\n")
}

func isPromptContentAlreadyWrapped(promptContent string) bool {
	content := strings.TrimSpace(promptContent)
	if content == "" {
		return false
	}
	return strings.Contains(content, "--- CONTEXT ENTRY BEGIN ---") &&
		strings.Contains(content, "--- CONTEXT ENTRY END ---") &&
		strings.Contains(content, "--- USER MESSAGE BEGIN ---") &&
		strings.Contains(content, "--- USER MESSAGE END ---") &&
		strings.Contains(content, "Current time:")
}

func ensurePromptContentWrapped(promptContent string) string {
	if strings.TrimSpace(promptContent) == "" {
		return promptContent
	}
	if isPromptContentAlreadyWrapped(promptContent) {
		return promptContent
	}
	return buildPromptContent(promptContent)
}

// BuildKiroPayload builds the complete Kiro API payload.
func BuildKiroPayload(
	messages []UnifiedMessage,
	systemPrompt string,
	modelID string,
	tools []UnifiedTool,
	conversationID string,
	profileArn string,
	thinking *AnthropicThinking,
	outputConfig *AnthropicOutputCfg,
) (*KiroPayloadResult, error) {

	processedTools, toolDoc := ProcessToolsWithLongDescriptions(tools)
	if err := ValidateToolNames(processedTools); err != nil {
		return nil, err
	}

	fullSystemPrompt := systemPrompt
	if toolDoc != "" {
		if fullSystemPrompt != "" {
			fullSystemPrompt += toolDoc
		} else {
			fullSystemPrompt = strings.TrimSpace(toolDoc)
		}
	}

	var processedMessages []UnifiedMessage
	if len(tools) == 0 {
		stripped, _ := StripAllToolContent(messages)
		processedMessages = stripped
	} else {
		processedMessages = messages
	}

	processedMessages = EnsureFirstMessageIsUser(processedMessages)
	processedMessages = NormalizeMessageRoles(processedMessages)

	// Handle prefill: if last message is assistant, truncate to last user message
	if len(processedMessages) > 0 && processedMessages[len(processedMessages)-1].Role != "user" {
		lastUserIdx := -1
		for i := len(processedMessages) - 1; i >= 0; i-- {
			if processedMessages[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx >= 0 {
			processedMessages = processedMessages[:lastUserIdx+1]
		} else {
			return nil, fmt.Errorf("no user message found")
		}
	}

	if len(processedMessages) == 0 {
		return nil, fmt.Errorf("no messages to send")
	}

	var currentMsg UnifiedMessage
	var historyMessages []UnifiedMessage
	if len(processedMessages) > 0 {
		currentMsg = processedMessages[len(processedMessages)-1]
		if len(processedMessages) > 1 {
			historyMessages = processedMessages[:len(processedMessages)-1]
		}
	}

	if len(historyMessages) > 0 {
		historyMessages = MergeAdjacentMessages(historyMessages)
	}

	thinkingPrefix := generateThinkingPrefix(thinking, outputConfig)

	wrappedSystemPrompt := fullSystemPrompt
	if thinkingPrefix != "" && !hasThinkingTags(wrappedSystemPrompt) {
		if wrappedSystemPrompt != "" {
			wrappedSystemPrompt = thinkingPrefix + "\n" + wrappedSystemPrompt
		} else {
			wrappedSystemPrompt = thinkingPrefix
		}
	}

	var systemHistory []UnifiedMessage
	if strings.TrimSpace(wrappedSystemPrompt) != "" {
		systemHistory = append(systemHistory, UnifiedMessage{
			Role:    "user",
			Content: buildContextOnlyContent(wrappedSystemPrompt),
		})
		systemHistory = append(systemHistory, UnifiedMessage{
			Role:    "assistant",
			Content: systemAssistantAck,
		})
	}

	for i := range historyMessages {
		if historyMessages[i].Role == "user" && strings.TrimSpace(historyMessages[i].Content) != "" {
			historyMessages[i].Content = ensurePromptContentWrapped(historyMessages[i].Content)
		}
	}

	envState := buildEnvState()
	allHistoryMessages := append(systemHistory, historyMessages...)
	history := BuildKiroHistory(allHistoryMessages, "", kiroCLIOrigin, envState)
	history = limitHistoryMessages(history, maxHistoryMessages, len(systemHistory)/2)

	currentMessage := currentMsg
	currentContent := currentMessage.Content
	if currentMessage.Role == "user" && strings.TrimSpace(currentContent) != "" {
		currentContent = ensurePromptContentWrapped(currentContent)
	}

	if currentMessage.Role == "assistant" {
		assistant := &KiroAssistantResponseMessage{Content: currentContent}
		if shouldAssignAssistantMessageID(currentContent, nil) {
			assistant.MessageID = generateUUID()
		}
		history = append(history, KiroHistoryMessage{
			AssistantResponseMessage: assistant,
		})
		history = limitHistoryMessages(history, maxHistoryMessages, len(systemHistory)/2)
		currentContent = ""
	}

	var kiroImages []KiroImage
	if len(currentMessage.Images) > 0 {
		kiroImages = ConvertImagesToKiroFormat(currentMessage.Images)
	}

	validatedToolResults, orphanedToolUseIDs := validateToolPairing(history, currentMessage.ToolResults)
	history = removeOrphanedToolUses(history, orphanedToolUseIDs)

	historyToolNames := collectHistoryToolNames(history)
	existingToolNames := make(map[string]bool)
	for _, tool := range processedTools {
		existingToolNames[strings.ToLower(tool.Name)] = true
	}

	for _, toolName := range historyToolNames {
		if !existingToolNames[strings.ToLower(toolName)] {
			processedTools = append(processedTools, createPlaceholderTool(toolName))
		}
	}

	userInputCtx := &KiroUserInputMessageContext{EnvState: envState}
	kiroTools := ConvertToolsToKiroFormat(processedTools)
	if len(kiroTools) > 0 {
		userInputCtx.Tools = kiroTools
	}
	if len(validatedToolResults) > 0 {
		kiroResults := ConvertToolResultsToKiroFormat(validatedToolResults)
		if len(kiroResults) > 0 {
			userInputCtx.ToolResults = kiroResults
		}
	}

	userInputMsg := &KiroUserInputMessage{
		Content:                 currentContent,
		ModelID:                 modelID,
		Origin:                  kiroCLIOrigin,
		UserInputMessageContext: userInputCtx,
	}
	if len(kiroImages) > 0 {
		userInputMsg.Images = kiroImages
	}

	payload := &KiroPayload{
		ConversationState: KiroConversationState{
			AgentContinuationID: generateUUID(),
			AgentTaskType:       "vibe",
			ChatTriggerType:     "MANUAL",
			ConversationID:      conversationID,
			CurrentMessage: KiroCurrentMessage{
				UserInputMessage: userInputMsg,
			},
		},
	}
	if len(history) > 0 {
		payload.ConversationState.History = history
	}
	if profileArn != "" {
		payload.ProfileArn = profileArn
	}

	return &KiroPayloadResult{
		Payload:           payload,
		ToolDocumentation: toolDoc,
	}, nil
}
