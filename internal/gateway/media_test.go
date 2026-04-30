package gateway

import "testing"

func TestExtractMediaReferencesFindsURLsAndBase64(t *testing.T) {
	body := []byte(`{
	  "messages":[
	    {"content":[
	      {"type":"image_url","image_url":{"url":"https://example.test/a.png"}},
	      {"type":"input_audio","input_audio":{"data":"aGVsbG8="}}
	    ]}
	  ],
	  "image":"data:image/png;base64,aGVsbG8="
	}`)

	refs := extractMediaReferences(body)
	if len(refs) != 3 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].URL != "https://example.test/a.png" {
		t.Fatalf("first ref = %#v", refs[0])
	}
	if refs[1].Base64Data != "aGVsbG8=" || refs[2].MediaType != "image/png" {
		t.Fatalf("base64 refs = %#v", refs)
	}
}
