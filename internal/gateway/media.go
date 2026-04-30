package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type MediaReference struct {
	URL        string
	Base64Data string
	MediaType  string
	SourcePath string
}

type mediaJSONMember struct {
	key   string
	value any
}

type mediaJSONObject []mediaJSONMember

func extractMediaReferences(body []byte) []MediaReference {
	if len(body) == 0 {
		return nil
	}
	root, err := decodeOrderedJSON(body)
	if err != nil {
		return nil
	}
	refs := []MediaReference{}
	walkMedia(root, "$", &refs)
	return refs
}

func decodeOrderedJSON(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	value, err := decodeOrderedValue(decoder)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("unexpected trailing JSON token %v", token)
	}
	return value, nil
}

func decodeOrderedValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			object := mediaJSONObject{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("unexpected JSON object key %v", keyToken)
				}
				value, err := decodeOrderedValue(decoder)
				if err != nil {
					return nil, err
				}
				object = append(object, mediaJSONMember{key: key, value: value})
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			array := []any{}
			for decoder.More() {
				value, err := decodeOrderedValue(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return array, nil
		}
	}
	return token, nil
}

func walkMedia(value any, path string, refs *[]MediaReference) {
	switch typed := value.(type) {
	case mediaJSONObject:
		for _, member := range typed {
			childPath := path + "." + member.key
			if member.key == "url" {
				if url, ok := member.value.(string); ok && strings.HasPrefix(url, "http") {
					*refs = append(*refs, MediaReference{URL: url, SourcePath: childPath})
				}
			}
			if isBase64MediaField(member.key, childPath, member.value) {
				if encoded, mediaType := parseBase64Media(member.value); encoded != "" {
					*refs = append(*refs, MediaReference{Base64Data: encoded, MediaType: mediaType, SourcePath: childPath})
				}
			}
			walkMedia(member.value, childPath, refs)
		}
	case map[string]any:
		for key, child := range typed {
			childPath := path + "." + key
			if key == "url" {
				if url, ok := child.(string); ok && strings.HasPrefix(url, "http") {
					*refs = append(*refs, MediaReference{URL: url, SourcePath: childPath})
				}
			}
			if isBase64MediaField(key, childPath, child) {
				if encoded, mediaType := parseBase64Media(child); encoded != "" {
					*refs = append(*refs, MediaReference{Base64Data: encoded, MediaType: mediaType, SourcePath: childPath})
				}
			}
			walkMedia(child, childPath, refs)
		}
	case []any:
		for index, child := range typed {
			walkMedia(child, path+"["+strconv.Itoa(index)+"]", refs)
		}
	}
}

func isBase64MediaField(key, path string, value any) bool {
	if text, ok := value.(string); ok && strings.HasPrefix(text, "data:") {
		return true
	}
	if key == "b64_json" {
		return true
	}
	if key == "data" {
		return strings.Contains(path, ".input_audio.") ||
			strings.Contains(path, ".audio.") ||
			strings.Contains(path, ".input_image.") ||
			strings.Contains(path, ".image.")
	}
	if key == "image" {
		return true
	}
	return false
}

func parseBase64Media(value any) (string, string) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", ""
	}
	if strings.HasPrefix(text, "data:") {
		header, data, ok := strings.Cut(text, ",")
		if !ok || !strings.Contains(header, ";base64") {
			return "", ""
		}
		if !isValidBase64(data) {
			return "", ""
		}
		mediaType := strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
		return data, mediaType
	}
	if isValidBase64(text) {
		return text, ""
	}
	return "", ""
}

func isValidBase64(text string) bool {
	if len(text) < 8 || strings.ContainsAny(text, " \n\r\t{}[]") {
		return false
	}
	if _, err := base64.StdEncoding.Strict().DecodeString(text); err == nil {
		return true
	}
	if strings.Contains(text, "=") {
		return false
	}
	_, err := base64.RawStdEncoding.Strict().DecodeString(text)
	return err == nil
}
