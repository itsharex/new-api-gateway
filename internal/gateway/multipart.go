package gateway

import (
	"mime"
	"net/http"
	"strings"
)

func isMultipart(req *http.Request) bool {
	if req == nil {
		return false
	}

	contentType := req.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "multipart/") {
		return true
	}
	if err != nil {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "multipart/")
	}
	return false
}
