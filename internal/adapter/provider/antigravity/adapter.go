package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Bowl42/maxx-next/internal/adapter/provider"
	"github.com/Bowl42/maxx-next/internal/converter"
	ctxutil "github.com/Bowl42/maxx-next/internal/context"
	"github.com/Bowl42/maxx-next/internal/cooldown"
	"github.com/Bowl42/maxx-next/internal/domain"
	"github.com/Bowl42/maxx-next/internal/usage"
)

func init() {
	provider.RegisterAdapterFactory("antigravity", NewAdapter)
}

// TokenCache caches access tokens
type TokenCache struct {
	AccessToken string
	ExpiresAt   time.Time
}

type AntigravityAdapter struct {
	provider   *domain.Provider
	converter  *converter.Registry
	tokenCache *TokenCache
	tokenMu    sync.RWMutex
}

func NewAdapter(p *domain.Provider) (provider.ProviderAdapter, error) {
	if p.Config == nil || p.Config.Antigravity == nil {
		return nil, fmt.Errorf("provider %s missing antigravity config", p.Name)
	}
	return &AntigravityAdapter{
		provider:   p,
		converter:  converter.NewRegistry(),
		tokenCache: &TokenCache{},
	}, nil
}

func (a *AntigravityAdapter) SupportedClientTypes() []domain.ClientType {
	// Antigravity natively supports Claude, OpenAI, and Gemini by converting to Gemini/v1internal API
	return []domain.ClientType{domain.ClientTypeClaude, domain.ClientTypeOpenAI, domain.ClientTypeGemini}
}

func (a *AntigravityAdapter) Execute(ctx context.Context, w http.ResponseWriter, req *http.Request, provider *domain.Provider) error {
	clientType := ctxutil.GetClientType(ctx)
	requestModel := ctxutil.GetRequestModel(ctx) // Original model from request (e.g., "claude-3-5-sonnet-20241022-online")
	mappedModel := ctxutil.GetMappedModel(ctx)   // Mapped model after route resolution
	requestBody := ctxutil.GetRequestBody(ctx)

	// [Model Mapping] Apply Antigravity model mapping (like Antigravity-Manager)
	// Only map if route didn't provide a mapping (mappedModel empty or same as request)
	config := provider.Config.Antigravity
	if mappedModel == "" || mappedModel == requestModel {
		// Route didn't provide mapping, use our internal mapping with haikuTarget config
		haikuTarget := ""
		if config != nil {
			haikuTarget = config.HaikuTarget
		}
		mappedModel = MapClaudeModelToGeminiWithConfig(requestModel, haikuTarget)
	}
	// If route provided a different mappedModel, trust it and don't re-map
	// (user/route has explicitly configured the target model)

	// Get streaming flag from context (already detected correctly for Gemini URL path)
	stream := ctxutil.GetIsStream(ctx)

	// Get access token
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return domain.NewProxyErrorWithMessage(err, true, "failed to get access token")
	}

	// Antigravity uses Gemini format
	targetType := domain.ClientTypeGemini
	needsConversion := clientType != targetType

	// Transform request if needed
	var geminiBody []byte
	if needsConversion {
		geminiBody, err = a.converter.TransformRequest(clientType, targetType, requestBody, mappedModel, stream)
		if err != nil {
			return domain.NewProxyErrorWithMessage(domain.ErrFormatConversion, true, "failed to transform request")
		}
	} else {
		// For Gemini, unwrap CLI envelope if present
		geminiBody = unwrapGeminiCLIEnvelope(requestBody)
	}

	// [SessionID Support] Extract metadata.user_id from original request for sessionId (like Antigravity-Manager)
	sessionID := extractSessionID(requestBody)

	// [Post-Processing] Apply Claude request post-processing (like CLIProxyAPI)
	// - Inject interleaved thinking hint when tools + thinking enabled
	// - Use cached signatures for thinking blocks
	// - Apply skip_thought_signature_validator for tool calls without valid signatures
	// - Smart thinking downgrade when history is incompatible
	if clientType == domain.ClientTypeClaude {
		// Determine if thinking should be enabled (considering model defaults like Opus 4.5)
		hasThinking := HasThinkingEnabledWithModel(requestBody, mappedModel)

		// Check if target model supports thinking
		if hasThinking && !TargetModelSupportsThinking(mappedModel) {
			hasThinking = false
		}

		// Check if thinking should be disabled due to history (like Antigravity-Manager)
		// Scenario: last Assistant message has ToolUse but no Thinking block
		if hasThinking {
			var req map[string]interface{}
			if err := json.Unmarshal(geminiBody, &req); err == nil {
				if contents, ok := req["contents"].([]interface{}); ok {
					if ShouldDisableThinkingDueToHistory(contents) {
						hasThinking = false
					}
				}
			}
		}

		geminiBody = PostProcessClaudeRequest(geminiBody, sessionID, hasThinking, requestBody, mappedModel)
	}

	// Wrap request in v1internal format
	upstreamBody, err := wrapV1InternalRequest(geminiBody, config.ProjectID, requestModel, mappedModel, sessionID)
	if err != nil {
		return domain.NewProxyErrorWithMessage(domain.ErrFormatConversion, true, "failed to wrap request for v1internal")
	}

	// Build upstream URL (v1internal endpoint)
	upstreamURL := a.buildUpstreamURL(stream)

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		return domain.NewProxyErrorWithMessage(domain.ErrUpstreamError, true, "failed to create upstream request")
	}

	// Set only the required headers (like Antigravity-Manager)
	// DO NOT copy any client headers - they may contain API keys or other sensitive data
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
	upstreamReq.Header.Set("User-Agent", AntigravityUserAgent)

	// Capture request info for attempt record
	if attempt := ctxutil.GetUpstreamAttempt(ctx); attempt != nil {
		attempt.RequestInfo = &domain.RequestInfo{
			Method:  upstreamReq.Method,
			URL:     upstreamURL,
			Headers: flattenHeaders(upstreamReq.Header),
			Body:    string(upstreamBody),
		}
	}

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(upstreamReq)
	if err != nil {
		return domain.NewProxyErrorWithMessage(domain.ErrUpstreamError, true, "failed to connect to upstream")
	}
	defer resp.Body.Close()

	// Check for 401 (token expired) and retry once
	if resp.StatusCode == http.StatusUnauthorized {
		// Invalidate token cache
		a.tokenMu.Lock()
		a.tokenCache = &TokenCache{}
		a.tokenMu.Unlock()

		// Get new token
		accessToken, err = a.getAccessToken(ctx)
		if err != nil {
			return domain.NewProxyErrorWithMessage(err, true, "failed to refresh access token")
		}

		// Retry request with only required headers
		upstreamReq, _ = http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewReader(upstreamBody))
		upstreamReq.Header.Set("Content-Type", "application/json")
		upstreamReq.Header.Set("Authorization", "Bearer "+accessToken)
		upstreamReq.Header.Set("User-Agent", AntigravityUserAgent)
		resp, err = client.Do(upstreamReq)
		if err != nil {
			return domain.NewProxyErrorWithMessage(domain.ErrUpstreamError, true, "failed to connect to upstream after token refresh")
		}
		defer resp.Body.Close()
	}

	// Check for error response
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		// Capture error response info
		if attempt := ctxutil.GetUpstreamAttempt(ctx); attempt != nil {
			attempt.ResponseInfo = &domain.ResponseInfo{
				Status:  resp.StatusCode,
				Headers: flattenHeaders(resp.Header),
				Body:    string(body),
			}
		}

		// Check for RESOURCE_EXHAUSTED (429) and handle cooldown
		if resp.StatusCode == http.StatusTooManyRequests {
			a.handleResourceExhausted(ctx, body, provider)
		}

		// Parse retry info for 429/5xx responses (like Antigravity-Manager)
		var retryAfter time.Duration
		if retryInfo := ParseRetryInfo(resp.StatusCode, body); retryInfo != nil {
			// Apply jitter to prevent thundering herd
			retryAfter = ApplyJitter(retryInfo.Delay)
		}

		proxyErr := domain.NewProxyErrorWithMessage(
			fmt.Errorf("upstream error: %s", string(body)),
			isRetryableStatusCode(resp.StatusCode),
			fmt.Sprintf("upstream returned status %d", resp.StatusCode),
		)

		// Set retry info on error for upstream handling
		if retryAfter > 0 {
			proxyErr.RetryAfter = retryAfter
		}

		return proxyErr
	}

	// Handle response
	if stream {
		return a.handleStreamResponse(ctx, w, resp, clientType, targetType, needsConversion)
	}
	return a.handleNonStreamResponse(ctx, w, resp, clientType, targetType, needsConversion)
}

func (a *AntigravityAdapter) getAccessToken(ctx context.Context) (string, error) {
	// Check cache
	a.tokenMu.RLock()
	if a.tokenCache.AccessToken != "" && time.Now().Before(a.tokenCache.ExpiresAt) {
		token := a.tokenCache.AccessToken
		a.tokenMu.RUnlock()
		return token, nil
	}
	a.tokenMu.RUnlock()

	// Refresh token
	config := a.provider.Config.Antigravity
	accessToken, expiresIn, err := refreshGoogleToken(ctx, config.RefreshToken)
	if err != nil {
		return "", err
	}

	// Cache token
	a.tokenMu.Lock()
	a.tokenCache = &TokenCache{
		AccessToken: accessToken,
		ExpiresAt:   time.Now().Add(time.Duration(expiresIn-60) * time.Second), // 60s buffer
	}
	a.tokenMu.Unlock()

	return accessToken, nil
}

func refreshGoogleToken(ctx context.Context, refreshToken string) (string, int, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	data.Set("client_id", OAuthClientID)
	data.Set("client_secret", OAuthClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("token refresh failed: %s", string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, err
	}

	return result.AccessToken, result.ExpiresIn, nil
}

// v1internal endpoint (same as Antigravity-Manager)
const (
	V1InternalBaseURL = "https://cloudcode-pa.googleapis.com/v1internal"
)

func (a *AntigravityAdapter) buildUpstreamURL(stream bool) string {
	if stream {
		return fmt.Sprintf("%s:streamGenerateContent?alt=sse", V1InternalBaseURL)
	}
	return fmt.Sprintf("%s:generateContent", V1InternalBaseURL)
}

func (a *AntigravityAdapter) handleNonStreamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, clientType, targetType domain.ClientType, needsConversion bool) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.NewProxyErrorWithMessage(domain.ErrUpstreamError, true, "failed to read upstream response")
	}

	// Unwrap v1internal response wrapper (extract "response" field)
	unwrappedBody := unwrapV1InternalResponse(body)

	// Capture response info and extract token usage
	if attempt := ctxutil.GetUpstreamAttempt(ctx); attempt != nil {
		attempt.ResponseInfo = &domain.ResponseInfo{
			Status:  resp.StatusCode,
			Headers: flattenHeaders(resp.Header),
			Body:    string(body), // Keep original for debugging
		}

		// Extract token usage from unwrapped response
		if metrics := usage.ExtractFromResponse(string(unwrappedBody)); metrics != nil {
			attempt.InputTokenCount = metrics.InputTokens
			attempt.OutputTokenCount = metrics.OutputTokens
			attempt.CacheReadCount = metrics.CacheReadCount
			attempt.CacheWriteCount = metrics.CacheCreationCount
			attempt.Cache5mWriteCount = metrics.Cache5mCreationCount
			attempt.Cache1hWriteCount = metrics.Cache1hCreationCount
		}

		// Broadcast attempt update with token info
		if bc := ctxutil.GetBroadcaster(ctx); bc != nil {
			bc.BroadcastProxyUpstreamAttempt(attempt)
		}
	}

	var responseBody []byte

	// Use specialized Claude response conversion (like Antigravity-Manager)
	if clientType == domain.ClientTypeClaude {
		requestModel := ctxutil.GetRequestModel(ctx)
		responseBody, err = convertGeminiToClaudeResponse(unwrappedBody, requestModel)
		if err != nil {
			return domain.NewProxyErrorWithMessage(domain.ErrFormatConversion, false, "failed to transform response")
		}
	} else if needsConversion {
		responseBody, err = a.converter.TransformResponse(targetType, clientType, unwrappedBody)
		if err != nil {
			return domain.NewProxyErrorWithMessage(domain.ErrFormatConversion, false, "failed to transform response")
		}
	} else {
		responseBody = unwrappedBody
	}

	// Copy upstream headers (except those we override)
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(responseBody)
	return nil
}

func (a *AntigravityAdapter) handleStreamResponse(ctx context.Context, w http.ResponseWriter, resp *http.Response, clientType, targetType domain.ClientType, needsConversion bool) error {
	attempt := ctxutil.GetUpstreamAttempt(ctx)

	// Capture response info (for streaming, we only capture status and headers)
	if attempt != nil {
		attempt.ResponseInfo = &domain.ResponseInfo{
			Status:  resp.StatusCode,
			Headers: flattenHeaders(resp.Header),
			Body:    "[streaming]",
		}
	}

	// Copy upstream headers (except those we override)
	copyResponseHeaders(w.Header(), resp.Header)

	// Set/override streaming headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return domain.NewProxyErrorWithMessage(domain.ErrUpstreamError, false, "streaming not supported")
	}

	// Use specialized Claude SSE handler for Claude clients
	isClaudeClient := clientType == domain.ClientTypeClaude

	// Extract sessionID for signature caching (like CLIProxyAPI)
	requestBody := ctxutil.GetRequestBody(ctx)
	sessionID := extractSessionID(requestBody)

	// Get original request model for Claude response (like Antigravity-Manager)
	requestModel := ctxutil.GetRequestModel(ctx)

	var state *converter.TransformState
	var claudeState *ClaudeStreamingState
	if isClaudeClient {
		claudeState = NewClaudeStreamingStateWithSession(sessionID, requestModel)
	} else if needsConversion {
		state = converter.NewTransformState()
	}

	// Collect all SSE events for response body and token extraction
	var sseBuffer strings.Builder

	// Helper to extract tokens and update attempt with final response body
	extractTokens := func() {
		if attempt != nil && sseBuffer.Len() > 0 {
			// Update response body with collected SSE content
			if attempt.ResponseInfo != nil {
				attempt.ResponseInfo.Body = sseBuffer.String()
			}
			// Extract token usage
			if metrics := usage.ExtractFromStreamContent(sseBuffer.String()); metrics != nil {
				attempt.InputTokenCount = metrics.InputTokens
				attempt.OutputTokenCount = metrics.OutputTokens
				attempt.CacheReadCount = metrics.CacheReadCount
				attempt.CacheWriteCount = metrics.CacheCreationCount
				attempt.Cache5mWriteCount = metrics.Cache5mCreationCount
				attempt.Cache1hWriteCount = metrics.Cache1hCreationCount
			}
			// Broadcast attempt update with token info
			if bc := ctxutil.GetBroadcaster(ctx); bc != nil {
				bc.BroadcastProxyUpstreamAttempt(attempt)
			}
		}
	}

	// Use buffer-based approach like Antigravity-Manager
	// Read chunks and accumulate until we have complete lines
	var lineBuffer bytes.Buffer
	buf := make([]byte, 4096)

	for {
		// Check context before reading
		select {
		case <-ctx.Done():
			extractTokens()
			return domain.NewProxyErrorWithMessage(ctx.Err(), false, "client disconnected")
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			lineBuffer.Write(buf[:n])

			// Process complete lines (lines ending with \n)
			for {
				line, readErr := lineBuffer.ReadString('\n')
				if readErr != nil {
					// No complete line yet, put partial data back
					lineBuffer.WriteString(line)
					break
				}

				// Collect for token extraction
				sseBuffer.WriteString(line)

				// Process the complete line
				lineBytes := []byte(line)

				// Unwrap v1internal SSE chunk before processing
				unwrappedLine := unwrapV1InternalSSEChunk(lineBytes)

				var output []byte
				if isClaudeClient {
					// Use specialized Claude SSE transformation
					output = claudeState.ProcessGeminiSSELine(string(unwrappedLine))
				} else if needsConversion {
					// Transform the chunk using generic converter
					transformed, transformErr := a.converter.TransformStreamChunk(targetType, clientType, unwrappedLine, state)
					if transformErr != nil {
						continue // Skip malformed chunks
					}
					output = transformed
				} else {
					output = unwrappedLine
				}

				if len(output) > 0 {
					_, writeErr := w.Write(output)
					if writeErr != nil {
						// Client disconnected
						extractTokens()
						return domain.NewProxyErrorWithMessage(writeErr, false, "client disconnected")
					}
					flusher.Flush()
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				// Ensure Claude clients get termination events
				if isClaudeClient && claudeState != nil {
					if forceStop := claudeState.EmitForceStop(); len(forceStop) > 0 {
						_, _ = w.Write(forceStop)
						flusher.Flush()
					}
				}
				extractTokens()
				return nil
			}
			// Upstream connection closed - check if client is still connected
			if ctx.Err() != nil {
				// Try to send termination events for Claude clients
				if isClaudeClient && claudeState != nil {
					if forceStop := claudeState.EmitForceStop(); len(forceStop) > 0 {
						_, _ = w.Write(forceStop)
						flusher.Flush()
					}
				}
				extractTokens()
				return domain.NewProxyErrorWithMessage(ctx.Err(), false, "client disconnected")
			}
			// Ensure Claude clients get termination events
			if isClaudeClient && claudeState != nil {
				if forceStop := claudeState.EmitForceStop(); len(forceStop) > 0 {
					_, _ = w.Write(forceStop)
					flusher.Flush()
				}
			}
			extractTokens()
			return nil
		}
	}
}

// handleResourceExhausted handles 429 RESOURCE_EXHAUSTED errors with QUOTA_EXHAUSTED reason
// Only triggers cooldown when the error contains quotaResetTimeStamp in details
func (a *AntigravityAdapter) handleResourceExhausted(ctx context.Context, body []byte, provider *domain.Provider) {
	// Parse error response to check if it's QUOTA_EXHAUSTED with reset timestamp
	var errResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Type     string `json:"@type"`
				Reason   string `json:"reason,omitempty"`
				Metadata struct {
					Model               string `json:"model,omitempty"`
					QuotaResetDelay     string `json:"quotaResetDelay,omitempty"`
					QuotaResetTimeStamp string `json:"quotaResetTimeStamp,omitempty"`
				} `json:"metadata,omitempty"`
			} `json:"details,omitempty"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &errResp); err != nil {
		// Can't parse error, don't set cooldown
		return
	}

	// Check if it's RESOURCE_EXHAUSTED
	if errResp.Error.Status != "RESOURCE_EXHAUSTED" {
		return
	}

	// Look for QUOTA_EXHAUSTED with quotaResetTimeStamp in details
	var resetTime time.Time
	for _, detail := range errResp.Error.Details {
		if detail.Reason == "QUOTA_EXHAUSTED" && detail.Metadata.QuotaResetTimeStamp != "" {
			parsed, err := time.Parse(time.RFC3339, detail.Metadata.QuotaResetTimeStamp)
			if err == nil {
				resetTime = parsed
				break
			}
		}
	}

	if resetTime.IsZero() {
		// No quota reset timestamp found, query quota API
		config := provider.Config.Antigravity
		if config == nil {
			return
		}

		// Fetch quota in background to not block the response
		go func() {
			quotaCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			quota, err := FetchQuotaForProvider(quotaCtx, config.RefreshToken, config.ProjectID)
			if err != nil {
				// Failed to fetch quota, apply short cooldown
				cooldown.Default().SetCooldownDuration(provider.ID, time.Minute)
				return
			}

			// Check if any model has 0% quota
			var earliestReset time.Time
			hasZeroQuota := false

			for _, model := range quota.Models {
				if model.Percentage == 0 && model.ResetTime != "" {
					hasZeroQuota = true
					rt, err := time.Parse(time.RFC3339, model.ResetTime)
					if err != nil {
						continue
					}
					if earliestReset.IsZero() || rt.Before(earliestReset) {
						earliestReset = rt
					}
				}
			}

			if hasZeroQuota && !earliestReset.IsZero() {
				// Quota is 0, cooldown until reset time
				cooldown.Default().SetCooldown(provider.ID, earliestReset)
			} else {
				// Quota is not 0, apply short cooldown (1 minute)
				cooldown.Default().SetCooldownDuration(provider.ID, time.Minute)
			}
		}()
		return
	}

	// Found quota reset timestamp, set cooldown until that time
	cooldown.Default().SetCooldown(provider.ID, resetTime)
}