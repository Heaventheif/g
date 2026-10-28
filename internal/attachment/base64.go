// Package attachment contains provider-specific parsing primitives for
// Base64-encoded image and audio attachments.
package attachment

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// DataURIPolicy controls whether and how a data URI prefix is removed before
// decoding. It deliberately does not trim or otherwise normalize whitespace.
type DataURIPolicy uint8

const (
	// DataURINone leaves the complete input untouched. Data URIs therefore fail
	// normal Base64 decoding rather than being implicitly accepted.
	DataURINone DataURIPolicy = iota
	// DataURIBase64Only removes a prefix only when input begins with "data:" and
	// contains the exact ";base64," delimiter. It also returns the URI MIME type.
	DataURIBase64Only
	// DataURIThroughFirstComma removes everything through the first comma only
	// when input begins with "data:"; no metadata requirement is imposed.
	DataURIThroughFirstComma
)

// AlphabetPolicy controls padding acceptance. Go's StdEncoding decoder is used
// in both modes; RawStdEncoding is an explicit fallback only in the second.
type AlphabetPolicy uint8

const (
	// StdPaddedOnly accepts standard Base64 with required padding.
	StdPaddedOnly AlphabetPolicy = iota
	// StdPaddedOrRaw accepts standard padded Base64, then standard unpadded Base64.
	StdPaddedOrRaw
)

// ParseOptions makes URI and alphabet differences explicit at every call site.
type ParseOptions struct {
	DataURI  DataURIPolicy
	Alphabet AlphabetPolicy
}

// Image contains decoded image bytes and a MIME type found in an accepted data
// URI, if present. An empty MIME means the caller should apply its usual fallback.
type Image struct {
	Data []byte
	MIME string
}

// ParseImageBase64 applies the configured URI and alphabet policies to an image.
func ParseImageBase64(input string, options ParseOptions) (Image, error) {
	payload, mimeType, err := prepare(input, options)
	if err != nil {
		return Image{}, err
	}
	decoded, err := decode(payload, options.Alphabet)
	if err != nil {
		return Image{}, err
	}
	return Image{Data: decoded, MIME: mimeType}, nil
}

// ParseAudioBase64 applies the configured URI and alphabet policies to audio.
func ParseAudioBase64(input string, options ParseOptions) ([]byte, error) {
	payload, _, err := prepare(input, options)
	if err != nil {
		return nil, err
	}
	return decode(payload, options.Alphabet)
}

// EstimateStdDecodedLen returns encoding/base64.StdEncoding.DecodedLen for an
// input string. It is an upper-bound estimate (padding is not inspected), useful
// for preserving size guards when callers intentionally forward Base64 as-is.
func EstimateStdDecodedLen(input string) int {
	return base64.StdEncoding.DecodedLen(len(input))
}

func prepare(input string, options ParseOptions) (payload, mimeType string, err error) {
	payload = input
	switch options.DataURI {
	case DataURINone:
	case DataURIBase64Only:
		if strings.HasPrefix(input, "data:") {
			if idx := strings.Index(input, ";base64,"); idx >= 0 {
				mimeType = input[len("data:"):idx]
				payload = input[idx+len(";base64,"):]
			}
		}
	case DataURIThroughFirstComma:
		if strings.HasPrefix(input, "data:") {
			if idx := strings.IndexByte(input, ','); idx >= 0 {
				payload = input[idx+1:]
			}
		}
	default:
		return "", "", fmt.Errorf("unknown data URI policy %d", options.DataURI)
	}
	return payload, mimeType, nil
}

func decode(input string, policy AlphabetPolicy) ([]byte, error) {
	switch policy {
	case StdPaddedOnly:
		return base64.StdEncoding.DecodeString(input)
	case StdPaddedOrRaw:
		decoded, err := base64.StdEncoding.DecodeString(input)
		if err == nil {
			return decoded, nil
		}
		return base64.RawStdEncoding.DecodeString(input)
	default:
		return nil, fmt.Errorf("unknown Base64 alphabet policy %d", policy)
	}
}
