package conversation

// ContextUsage measures the last prepared prompt, before response generation.
// Reserved includes the response cap and a small special-token safety margin.
type ContextUsage struct {
	Limit           int `json:"limit_tokens"`
	PromptTokens    int `json:"prompt_tokens"`
	Reserved        int `json:"reserved_tokens"`
	DroppedMessages int `json:"dropped_messages"`
}
