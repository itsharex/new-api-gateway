package gateway

import (
	"mime"
	"net/http"
	"strings"
)

func isMultipart(req *http.Request) bool {
	contentType := req.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.HasPrefix(contentType, "multipart/")
	}
	return strings.HasPrefix(mediaType, "multipart/")
}
