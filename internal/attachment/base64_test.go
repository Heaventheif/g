package attachment

import (
	"bytes"
	"testing"
)

func TestParseImageBase64DataURIAndMIME(t *testing.T) {
	got, err := ParseImageBase64("data:image/png;base64,QUJD", ParseOptions{
		DataURI:  DataURIBase64Only,
		Alphabet: StdPaddedOrRaw,
	})
	if err != nil {
		t.Fatalf("ParseImageBase64 returned error: %v", err)
	}
	if !bytes.Equal(got.Data, []byte("ABC")) {
		t.Fatalf("data = %q, want ABC", got.Data)
	}
	if got.MIME != "image/png" {
		t.Fatalf("MIME = %q, want image/png", got.MIME)
	}
}

func TestParseImageBase64AcceptsRawStandardAlphabet(t *testing.T) {
	got, err := ParseImageBase64("QUI", ParseOptions{
		DataURI:  DataURINone,
		Alphabet: StdPaddedOrRaw,
	})
	if err != nil {
		t.Fatalf("ParseImageBase64 returned error: %v", err)
	}
	if !bytes.Equal(got.Data, []byte("AB")) {
		t.Fatalf("data = %q, want AB", got.Data)
	}
}

func TestParseImageBase64RequiresExactDataURIDelimiter(t *testing.T) {
	for _, input := range []string{
		"data:image/png,QUJD",
		"prefix-data:image/png;base64,QUJD",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseImageBase64(input, ParseOptions{
				DataURI:  DataURIBase64Only,
				Alphabet: StdPaddedOrRaw,
			}); err == nil {
				t.Fatalf("ParseImageBase64(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestParseAudioBase64URIPolicies(t *testing.T) {
	input := "data:anything,QUJD"
	for _, tc := range []struct {
		name    string
		policy  DataURIPolicy
		want    []byte
		wantErr bool
	}{
		{name: "strip everything before first comma", policy: DataURIThroughFirstComma, want: []byte("ABC")},
		{name: "do not strip data URI", policy: DataURINone, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAudioBase64(input, ParseOptions{DataURI: tc.policy, Alphabet: StdPaddedOnly})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && !bytes.Equal(got, tc.want) {
				t.Fatalf("data = %q, want %q", got, tc.want)
			}
		})
	}

	got, err := ParseAudioBase64("data:audio/wav;name=clip,QUJD", ParseOptions{
		DataURI:  DataURIThroughFirstComma,
		Alphabet: StdPaddedOnly,
	})
	if err != nil || !bytes.Equal(got, []byte("ABC")) {
		t.Fatalf("comma-only policy got (%q, %v), want ABC", got, err)
	}
}

func TestParseBase64AlphabetAndWhitespacePolicies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		policy  AlphabetPolicy
		want    []byte
		wantErr bool
	}{
		{name: "padded standard", input: "YQ==", policy: StdPaddedOnly, want: []byte("a")},
		{name: "unpadded standard allowed", input: "YQ", policy: StdPaddedOrRaw, want: []byte("a")},
		{name: "unpadded standard rejected", input: "YQ", policy: StdPaddedOnly, wantErr: true},
		{name: "URL alphabet rejected", input: "-_8=", policy: StdPaddedOrRaw, wantErr: true},
		{name: "spaces are not normalized", input: "QU JD", policy: StdPaddedOrRaw, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAudioBase64(tc.input, ParseOptions{DataURI: DataURINone, Alphabet: tc.policy})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && !bytes.Equal(got, tc.want) {
				t.Fatalf("data = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseEmptyBase64ReturnsEmptyPayload(t *testing.T) {
	got, err := ParseAudioBase64("", ParseOptions{DataURI: DataURINone, Alphabet: StdPaddedOnly})
	if err != nil {
		t.Fatalf("empty Base64 decode error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty Base64 decoded to %d bytes", len(got))
	}
}

func TestEstimateStdDecodedLenIsConservative(t *testing.T) {
	if got := EstimateStdDecodedLen("YQ=="); got != 3 {
		t.Fatalf("EstimateStdDecodedLen(padded one-byte input) = %d, want conservative estimate 3", got)
	}
	if got := EstimateStdDecodedLen(""); got != 0 {
		t.Fatalf("EstimateStdDecodedLen(empty) = %d, want 0", got)
	}
}
