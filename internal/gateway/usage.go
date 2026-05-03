package gateway

import "encoding/json"

// usageExtractor parses usage and model from one specific API format.
// Implementations are stateful for SSE: call processSSE per data line,
// then sseResult to read the accumulated state.
type usageExtractor interface {
	processSSE(payload []byte)
	sseResult() (minimalUsage, string)
	extractResponse(body []byte) (minimalUsage, string)
	extractRequest(path string, body []byte) string
}

type extractorFactory func() usageExtractor

var extractorFactories = map[string]extractorFactory{}

func registerExtractor(families []string, factory extractorFactory) {
	for _, f := range families {
		extractorFactories[f] = factory
	}
}

func extractorFor(family string) usageExtractor {
	if factory, ok := extractorFactories[family]; ok {
		return factory()
	}
	if factory, ok := extractorFactories["_generic"]; ok {
		return factory()
	}
	return nil
}

// extractModelFromBody is a shared helper used by most extractors.
func extractModelFromBody(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return v.Model
}
