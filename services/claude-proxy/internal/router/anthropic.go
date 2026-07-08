package router

// AnthropicResponse models the response payload from an Anthropic LLM call.
// Only the fields required by the router are defined.
type AnthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}
