package antigravity

import "strings"

// Claude to Gemini model mapping (like Antigravity-Manager)
var claudeToGeminiMap = map[string]string{
	// 直接支持的模型
	"claude-opus-4-5-thinking":   "claude-opus-4-5-thinking",
	"claude-sonnet-4-5":          "claude-sonnet-4-5",
	"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",

	// 别名映射
	"claude-sonnet-4-5-20250929": "claude-sonnet-4-5-thinking",
	"claude-3-5-sonnet-20241022": "claude-sonnet-4-5",
	"claude-3-5-sonnet-20240620": "claude-sonnet-4-5",
	"claude-opus-4":              "claude-opus-4-5-thinking",
	"claude-opus-4-5-20251101":   "claude-opus-4-5-thinking",

	// Haiku 映射 (like Antigravity-Manager: Haiku -> claude-sonnet-4-5)
	"claude-haiku-4":            "claude-sonnet-4-5",
	"claude-3-haiku-20240307":   "claude-sonnet-4-5",
	"claude-haiku-4-5-20251001": "claude-sonnet-4-5",

	// OpenAI 协议映射表
	"gpt-4":               "gemini-2.5-pro",
	"gpt-4-turbo":         "gemini-2.5-pro",
	"gpt-4-turbo-preview": "gemini-2.5-pro",
	"gpt-4-0125-preview":  "gemini-2.5-pro",
	"gpt-4-1106-preview":  "gemini-2.5-pro",
	"gpt-4-0613":          "gemini-2.5-pro",
	"gpt-4o":              "gemini-2.5-pro",
	"gpt-4o-2024-05-13":   "gemini-2.5-pro",
	"gpt-4o-2024-08-06":   "gemini-2.5-pro",
	"gpt-4o-mini":         "gemini-2.5-flash",
	"gpt-4o-mini-2024-07-18": "gemini-2.5-flash",
	"gpt-3.5-turbo":       "gemini-2.5-flash",
	"gpt-3.5-turbo-16k":   "gemini-2.5-flash",
	"gpt-3.5-turbo-0125":  "gemini-2.5-flash",
	"gpt-3.5-turbo-1106":  "gemini-2.5-flash",
	"gpt-3.5-turbo-0613":  "gemini-2.5-flash",

	// Gemini 协议映射表 (直接穿透)
	"gemini-2.5-flash-lite":     "gemini-2.5-flash-lite",
	"gemini-2.5-flash-thinking": "gemini-2.5-flash-thinking",
	"gemini-3-pro-low":          "gemini-3-pro-low",
	"gemini-3-pro-high":         "gemini-3-pro-high",
	"gemini-3-pro-preview":      "gemini-3-pro-preview",
	"gemini-3-pro":              "gemini-3-pro",
	"gemini-2.5-flash":          "gemini-2.5-flash",
	"gemini-2.5-pro":            "gemini-2.5-pro",
	"gemini-3-flash":            "gemini-3-flash",
	"gemini-3-pro-image":        "gemini-3-pro-image",
}

// MapClaudeModelToGemini maps Claude model names to Gemini model names
// (like Antigravity-Manager's map_claude_model_to_gemini)
func MapClaudeModelToGemini(input string) string {
	// Strip -online suffix for mapping lookup (will be re-added by resolveRequestConfig)
	cleanInput := strings.TrimSuffix(input, "-online")

	// 1. Check exact match in map
	if mapped, ok := claudeToGeminiMap[cleanInput]; ok {
		return mapped
	}

	// 2. Pass-through known prefixes (gemini-, -thinking) to support dynamic suffixes
	// (like Antigravity-Manager)
	if strings.HasPrefix(cleanInput, "gemini-") || strings.Contains(cleanInput, "thinking") {
		return cleanInput
	}

	// 3. Fallback to default
	return "claude-sonnet-4-5"
}

// ShouldEnableThinkingByDefault checks if thinking mode should be enabled by default
// Claude Code v2.0.67+ enables thinking by default for Opus 4.5 models
func ShouldEnableThinkingByDefault(model string) bool {
	modelLower := strings.ToLower(model)

	// Enable thinking by default for Opus 4.5 variants
	if strings.Contains(modelLower, "opus-4-5") || strings.Contains(modelLower, "opus-4.5") {
		return true
	}

	// Also enable for explicit thinking model variants
	if strings.Contains(modelLower, "-thinking") {
		return true
	}

	return false
}

// TargetModelSupportsThinking checks if the target model supports thinking mode
func TargetModelSupportsThinking(mappedModel string) bool {
	// Only models with "-thinking" suffix or Claude models support thinking
	return strings.Contains(mappedModel, "-thinking") || strings.HasPrefix(mappedModel, "claude-")
}

// ParseImageConfig parses image configuration from model name suffixes
// Returns imageConfig and cleanModelName
func ParseImageConfig(modelName string) (map[string]interface{}, string) {
	aspectRatio := "1:1"

	switch {
	case strings.Contains(modelName, "-21x9") || strings.Contains(modelName, "-21-9"):
		aspectRatio = "21:9"
	case strings.Contains(modelName, "-16x9") || strings.Contains(modelName, "-16-9"):
		aspectRatio = "16:9"
	case strings.Contains(modelName, "-9x16") || strings.Contains(modelName, "-9-16"):
		aspectRatio = "9:16"
	case strings.Contains(modelName, "-4x3") || strings.Contains(modelName, "-4-3"):
		aspectRatio = "4:3"
	case strings.Contains(modelName, "-3x4") || strings.Contains(modelName, "-3-4"):
		aspectRatio = "3:4"
	case strings.Contains(modelName, "-1x1") || strings.Contains(modelName, "-1-1"):
		aspectRatio = "1:1"
	}

	isHD := strings.Contains(modelName, "-4k") || strings.Contains(modelName, "-hd")
	is2K := strings.Contains(modelName, "-2k")

	config := map[string]interface{}{
		"aspectRatio": aspectRatio,
	}

	if isHD {
		config["imageSize"] = "4K"
	} else if is2K {
		config["imageSize"] = "2K"
	}

	// The upstream model must be EXACTLY "gemini-3-pro-image"
	return config, "gemini-3-pro-image"
}
