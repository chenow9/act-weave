package aapfile

import "testing"

func TestOutboundMediaTypesCompatible(t *testing.T) {
	t.Parallel()

	t.Run("text types accept weak sniff", func(t *testing.T) {
		for _, declared := range []string{"text/plain", "text/csv", "text/markdown", "application/json"} {
			if !outboundMediaTypesCompatible(declared, "text/plain") {
				t.Fatalf("%s sniffed as text/plain must be accepted", declared)
			}
			if !outboundMediaTypesCompatible(declared, "application/octet-stream") {
				t.Fatalf("%s sniffed as octet-stream must be accepted", declared)
			}
			if !outboundMediaTypesCompatible(declared, "") {
				t.Fatalf("%s empty sniff must be accepted", declared)
			}
			if !outboundMediaTypesCompatible(declared, declared) {
				t.Fatalf("%s exact sniff must be accepted", declared)
			}
		}
	})

	t.Run("csv declared with image magic denied", func(t *testing.T) {
		if outboundMediaTypesCompatible("text/csv", "image/png") {
			t.Fatal("declared text/csv + detected image/png must be denied")
		}
	})

	t.Run("inbound image rules unchanged", func(t *testing.T) {
		if !mediaTypesCompatible("image/png", "image/png") {
			t.Fatal("png exact")
		}
		if mediaTypesCompatible("image/png", "text/plain") {
			t.Fatal("inbound png must not accept text/plain")
		}
		if !outboundMediaTypesCompatible("image/png", "image/png") {
			t.Fatal("outbound png exact")
		}
		if outboundMediaTypesCompatible("image/png", "text/plain") {
			t.Fatal("outbound png must not accept text/plain")
		}
		if !outboundMediaTypesCompatible("image/jpeg", "image/jpg") {
			t.Fatal("jpeg alias")
		}
	})
}

func TestDetectMediaTypeWeakTextSniff(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body []byte
	}{
		{name: "csv", body: []byte("col_a,col_b\n1,2\n")},
		{name: "json", body: []byte(`{"ok":true,"items":[1,2,3]}`)},
		{name: "markdown", body: []byte("# Invoice\n\n- total: 12\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectMediaTypeFromSample(tc.body)
			if got != "text/plain" {
				t.Fatalf("DetectMediaTypeFromSample=%q want text/plain", got)
			}
		})
	}
}

func TestAllowedOutboundMediaType(t *testing.T) {
	t.Parallel()
	if !AllowedOutboundMediaType("text/csv") || !AllowedOutboundMediaType("application/json") {
		t.Fatal("outbound text types must be allowed")
	}
	if AllowedMediaType("text/csv") || AllowedMediaType("application/json") {
		t.Fatal("inbound allowlist must not include outbound-only text types")
	}
	if !AllowedMediaType(MediaTypeDocx) || !AllowedMediaType(MediaTypeXlsx) || !AllowedMediaType(MediaTypeZip) {
		t.Fatal("inbound allowlist must include Word, Excel, and zip")
	}
}

func TestOfficeAndZipMagicCompatibility(t *testing.T) {
	t.Parallel()
	zipBody := []byte{'P', 'K', 0x03, 0x04, 0x00, 0x00}
	if got := DetectMediaTypeFromSample(zipBody); got != MediaTypeZip {
		t.Fatalf("zip sniff=%q", got)
	}
	oleBody := []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 0x00}
	if got := DetectMediaTypeFromSample(oleBody); got != mediaTypeOLE {
		t.Fatalf("ole sniff=%q", got)
	}
	if !mediaTypesCompatible(MediaTypeDocx, MediaTypeZip) || !mediaTypesCompatible(MediaTypeXlsx, MediaTypeZip) {
		t.Fatal("docx/xlsx must accept zip magic")
	}
	if !mediaTypesCompatible(MediaTypeZip, MediaTypeZipAlt) {
		t.Fatal("zip aliases")
	}
	if !mediaTypesCompatible(MediaTypeDoc, mediaTypeOLE) || !mediaTypesCompatible(MediaTypeXls, mediaTypeOLE) {
		t.Fatal("legacy Office must accept OLE magic")
	}
	if mediaTypesCompatible(MediaTypeDocx, MediaTypePNG) {
		t.Fatal("docx must not accept png magic")
	}
	if mediaTypesCompatible("video/mp4", MediaTypeZip) {
		t.Fatal("unlisted types stay denied")
	}
}
