package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The structured call: one request whose answer must be a JSON value of a
// given schema. The author layer sends every request through it. It differs
// from ChatCompletion in what the caller controls — the participant's model
// and parameters, the output mode and a per-request timeout — and in what it
// returns: the standard OpenAI response fields only (model, content or tool
// arguments, finish reason, usage) and, on failure, a closed error class.
// ChatCompletion and its callers are untouched.

// OutputMode is how the schema reaches the model.
type OutputMode string

const (
	// OutputJSONSchema asks for strict Structured Outputs (response_format
	// json_schema).
	OutputJSONSchema OutputMode = "json_schema"
	// OutputJSONObject asks only for a JSON object (response_format
	// json_object); the schema travels in the prompt.
	OutputJSONObject OutputMode = "json_object"
	// OutputTool forces one call of a tool whose parameters are the schema.
	OutputTool OutputMode = "tool"
)

// OutputModes lists the closed set of output modes.
func OutputModes() []OutputMode { return []OutputMode{OutputJSONSchema, OutputJSONObject, OutputTool} }

// ErrNotConfigured marks a request on a client whose llm section allows no
// requests (config.LLMConfig.Configured).
var ErrNotConfigured = errors.New("llm: provider not configured")

// messageRoleSystem carries the instruction of a structured call.
const messageRoleSystem = "system"

// toolTypeFunction is the only tool type the structured call uses.
const toolTypeFunction = "function"

// insufficientQuota is OpenAI's error type and code for an account out of
// quota.
const insufficientQuota = "insufficient_quota"

// The request fields an output ceiling can travel in.
const (
	outputFieldMaxCompletion = "max_completion_tokens"
	outputFieldMaxTokens     = "max_tokens"
)

// structuredMaxBody caps the response bytes read from the endpoint.
const structuredMaxBody = 8 << 20

// StructuredRequest is one structured call.
type StructuredRequest struct {
	// Model is the model asked for; the shared section's model is not used.
	Model  string
	System string
	User   string
	Mode   OutputMode
	// SchemaName names the response format or the tool.
	SchemaName string
	// Schema is the JSON schema; unused in OutputJSONObject.
	Schema json.RawMessage
	// Temperature and ReasoningEffort are sent only when set.
	Temperature     *float64
	ReasoningEffort string
	// Timeout bounds this request alone, instead of the shared section's
	// timeout; zero leaves it bounded only by ctx.
	Timeout time.Duration
	// MaxOutputTokens is the output ceiling, reasoning included, sent in
	// OutputLimitField ("max_completion_tokens" or "max_tokens"); zero sends
	// none.
	MaxOutputTokens  int
	OutputLimitField string
}

// Usage is the response's usage object: the standard counts, the reasoning
// tokens when the endpoint reports them, and the object as sent (provider
// fields such as a credit count are kept there for reference only).
type Usage struct {
	Present          bool
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	ReasoningTokens  *int64
	Raw              json.RawMessage
}

// StructuredResponse is a successful structured call. Content is the message
// content in the JSON modes and the joined tool-call arguments in the tool
// mode; it is empty when the model produced neither.
type StructuredResponse struct {
	Model        string
	Content      []byte
	FinishReason string
	Usage        Usage
}

// ErrorClass is the closed class of a failed structured call.
type ErrorClass string

const (
	ErrorAuth           ErrorClass = "auth"
	ErrorQuotaExhausted ErrorClass = "quota_exhausted"
	ErrorRateLimited    ErrorClass = "rate_limited"
	ErrorBadRequest     ErrorClass = "bad_request"
	ErrorServer         ErrorClass = "server_error"
	ErrorTimeout        ErrorClass = "timeout"
	ErrorTransport      ErrorClass = "transport_error"
	ErrorMalformed      ErrorClass = "malformed_response"
	ErrorCanceled       ErrorClass = "canceled"
)

// CallError is a failed structured call. It carries no request or response
// content, so it is safe to log whole.
type CallError struct {
	Class      ErrorClass
	HTTPStatus int
	// RetryAfter is the endpoint's Retry-After, when it sent one.
	RetryAfter time.Duration
}

func (e *CallError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("llm: structured call failed: %s (HTTP %d)", e.Class, e.HTTPStatus)
	}
	return "llm: structured call failed: " + string(e.Class)
}

// structuredBody is the wire body of a structured request.
type structuredBody struct {
	Model           string          `json:"model"`
	Messages        []Message       `json:"messages"`
	Stream          bool            `json:"stream"`
	Temperature     *float64        `json:"temperature,omitempty"`
	MaxCompletion   int             `json:"max_completion_tokens,omitempty"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
	Tools           []toolSpec      `json:"tools,omitempty"`
	ToolChoice      *toolChoice     `json:"tool_choice,omitempty"`
}

type responseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *jsonSchemaSpec `json:"json_schema,omitempty"`
}

type jsonSchemaSpec struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type toolSpec struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name       string          `json:"name"`
	Strict     bool            `json:"strict"`
	Parameters json.RawMessage `json:"parameters"`
}

type toolChoice struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

// buildStructuredBody maps a request onto the wire body of its mode.
func buildStructuredBody(req *StructuredRequest) ([]byte, error) {
	body := structuredBody{
		Model: req.Model,
		Messages: []Message{
			{Role: messageRoleSystem, Content: req.System},
			{Role: messageRoleUser, Content: req.User},
		},
		Temperature:     req.Temperature,
		ReasoningEffort: req.ReasoningEffort,
	}
	switch req.OutputLimitField {
	case "", outputFieldMaxCompletion:
		body.MaxCompletion = req.MaxOutputTokens
	case outputFieldMaxTokens:
		body.MaxTokens = req.MaxOutputTokens
	default:
		return nil, fmt.Errorf("llm: unknown output limit field %q", req.OutputLimitField)
	}
	switch req.Mode {
	case OutputJSONSchema:
		body.ResponseFormat = &responseFormat{Type: string(OutputJSONSchema),
			JSONSchema: &jsonSchemaSpec{Name: req.SchemaName, Strict: true, Schema: req.Schema}}
	case OutputJSONObject:
		body.ResponseFormat = &responseFormat{Type: string(OutputJSONObject)}
	case OutputTool:
		body.Tools = []toolSpec{{Type: toolTypeFunction,
			Function: toolFunction{Name: req.SchemaName, Strict: true, Parameters: req.Schema}}}
		choice := &toolChoice{Type: toolTypeFunction}
		choice.Function.Name = req.SchemaName
		body.ToolChoice = choice
	default:
		return nil, fmt.Errorf("llm: unknown output mode %q", req.Mode)
	}
	return json.Marshal(body)
}

// structuredWire is the subset of a chat completion response the structured
// call reads.
type structuredWire struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   *string        `json:"content"`
			ToolCalls []toolCallWire `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
	Error *APIError       `json:"error"`
}

type toolCallWire struct {
	Index    *int `json:"index"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type usageWire struct {
	PromptTokens            *int64 `json:"prompt_tokens"`
	CompletionTokens        *int64 `json:"completion_tokens"`
	TotalTokens             *int64 `json:"total_tokens"`
	CompletionTokensDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// CompleteStructured sends one structured call. The Authorization header is
// sent only when a key is configured. The request is bounded by ctx and by
// req.Timeout; the shared section's timeout does not apply.
func (c *Client) CompleteStructured(ctx context.Context, req *StructuredRequest) (*StructuredResponse, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if !slices.Contains(OutputModes(), req.Mode) {
		return nil, fmt.Errorf("llm: unknown output mode %q", req.Mode)
	}
	payload, err := buildStructuredBody(req)
	if err != nil {
		return nil, err
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	// The URL is operator configuration (llm.base_url), never request input.
	// #nosec:G107 -- the endpoint comes from operator configuration
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("llm: building a structured request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.structuredHTTP.Do(httpReq)
	if err != nil {
		return nil, &CallError{Class: transportClass(ctx, err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, structuredMaxBody))
	if err != nil {
		return nil, &CallError{Class: transportClass(ctx, err), HTTPStatus: resp.StatusCode}
	}

	return interpretStructured(req, resp, raw)
}

// interpretStructured reads a structured call's response: a non-200 status
// as its class, a body that is not a chat completion as malformed, and
// otherwise the standard fields.
func interpretStructured(req *StructuredRequest, resp *http.Response, raw []byte) (*StructuredResponse, error) {
	var wire structuredWire
	decodeErr := json.Unmarshal(raw, &wire)
	if resp.StatusCode != http.StatusOK {
		return nil, &CallError{
			Class:      statusClass(resp.StatusCode, wire.Error),
			HTTPStatus: resp.StatusCode,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if decodeErr != nil {
		return nil, &CallError{Class: ErrorMalformed, HTTPStatus: resp.StatusCode}
	}
	if wire.Error != nil {
		class := ErrorServer
		if quotaError(wire.Error) {
			class = ErrorQuotaExhausted
		}
		return nil, &CallError{Class: class, HTTPStatus: resp.StatusCode}
	}
	if len(wire.Choices) == 0 {
		return nil, &CallError{Class: ErrorMalformed, HTTPStatus: resp.StatusCode}
	}

	choice := wire.Choices[0]
	out := &StructuredResponse{Model: wire.Model, FinishReason: choice.FinishReason, Usage: parseUsage(wire.Usage)}
	if req.Mode == OutputTool {
		out.Content = joinToolArguments(choice.Message.ToolCalls, req.SchemaName)
	} else if choice.Message.Content != nil {
		out.Content = []byte(*choice.Message.Content)
	}
	return out, nil
}

// joinToolArguments rebuilds the arguments of the one forced tool call. Some
// endpoints split a non-streamed tool call into stream-style fragments that
// share an index: the first names the function, the rest carry consecutive
// pieces of the arguments. Fragments are grouped by index in arrival order
// and their arguments concatenated. The call taken is the one index named
// after the requested tool; none, or more than one, is no content.
func joinToolArguments(calls []toolCallWire, name string) []byte {
	type joined struct {
		name string
		args strings.Builder
	}
	var order []int
	byIndex := map[int]*joined{}
	for pos, call := range calls {
		idx := pos
		if call.Index != nil {
			idx = *call.Index
		}
		j, ok := byIndex[idx]
		if !ok {
			j = &joined{}
			byIndex[idx] = j
			order = append(order, idx)
		}
		if j.name == "" {
			j.name = call.Function.Name
		}
		j.args.WriteString(call.Function.Arguments)
	}
	var found *joined
	for _, idx := range order {
		if byIndex[idx].name != name {
			continue
		}
		if found != nil {
			return nil
		}
		found = byIndex[idx]
	}
	if found == nil {
		return nil
	}
	return []byte(found.args.String())
}

// parseUsage reads the standard usage counts. A missing or unreadable usage
// object is reported as absent, never as zero tokens.
func parseUsage(raw json.RawMessage) Usage {
	var w usageWire
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &w) != nil ||
		w.PromptTokens == nil || w.CompletionTokens == nil {
		return Usage{}
	}
	u := Usage{Present: true, PromptTokens: *w.PromptTokens, CompletionTokens: *w.CompletionTokens, Raw: raw}
	if w.TotalTokens != nil {
		u.TotalTokens = *w.TotalTokens
	} else {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	if w.CompletionTokensDetails != nil && w.CompletionTokensDetails.ReasoningTokens != nil {
		r := *w.CompletionTokensDetails.ReasoningTokens
		u.ReasoningTokens = &r
	}
	return u
}

// statusClass maps a non-200 status onto its class. A 429 that says the
// account is out of quota (OpenAI's insufficient_quota) is quota, not rate.
func statusClass(status int, apiErr *APIError) ErrorClass {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrorAuth
	case status == http.StatusPaymentRequired:
		return ErrorQuotaExhausted
	case status == http.StatusTooManyRequests:
		if quotaError(apiErr) {
			return ErrorQuotaExhausted
		}
		return ErrorRateLimited
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooEarly:
		return ErrorServer
	case status >= 400 && status < 500:
		return ErrorBadRequest
	}
	return ErrorServer
}

func quotaError(apiErr *APIError) bool {
	return apiErr != nil && (apiErr.Code == insufficientQuota || apiErr.Type == insufficientQuota)
}

// transportClass tells a timeout or a cancellation from a broken transport.
func transportClass(ctx context.Context, err error) ErrorClass {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorTimeout
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return ErrorCanceled
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrorTimeout
	}
	return ErrorTransport
}

// retryAfter reads a Retry-After header: delay seconds or an HTTP date.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
