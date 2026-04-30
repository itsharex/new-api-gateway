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
	  "image":"data:image/png;base64,aGVsbG8=",
	  "data":"data:image/png;base64,aGVsbG8="
	}`)

	refs := extractMediaReferences(body)
	if len(refs) != 4 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].URL != "https://example.test/a.png" {
		t.Fatalf("first ref = %#v", refs[0])
	}
	if refs[1].Base64Data != "aGVsbG8=" || refs[2].MediaType != "image/png" || refs[3].MediaType != "image/png" {
		t.Fatalf("base64 refs = %#v", refs)
	}
}

func TestExtractMediaReferencesIgnoresOrdinaryDataStrings(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"data":"metadata"}`),
		[]byte(`{"data":"not-media-id"}`),
	} {
		if refs := extractMediaReferences(body); len(refs) != 0 {
			t.Fatalf("refs for %s = %#v, want none", body, refs)
		}
	}
}
