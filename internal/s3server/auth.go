package s3server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	authorizationHeader = "Authorization"
	amzDateHeader       = "X-Amz-Date"
	amzContentSHA256    = "X-Amz-Content-Sha256"
	dateHeader          = "Date"
)

type credentials struct {
	AccessKey string
	Region    string
	Service   string
	Request   string
}

type authResult struct {
	secretKey string
	info      *TokenInfo
}

func authenticateRequest(r *http.Request, fixedAccessKey, fixedSecretKey string, resolver TokenResolver) (*authResult, bool) {
	auth := r.Header.Get(authorizationHeader)
	if auth == "" {
		return nil, false
	}

	cred, signedHeaders, signature, ok := parseAuthorization(auth)
	if !ok {
		return nil, false
	}

	dateStr := r.Header.Get(amzDateHeader)
	if dateStr == "" {
		dateStr = r.Header.Get(dateHeader)
	}
	if dateStr == "" {
		return nil, false
	}

	t, err := time.Parse("20060102T150405Z", dateStr)
	if err != nil {
		t, err = time.Parse(http.TimeFormat, dateStr)
		if err != nil {
			return nil, false
		}
	}
	if time.Since(t) > 15*time.Minute {
		return nil, false
	}

	var secretKey string
	var info *TokenInfo

	if resolver != nil {
		resolvedSecret, resolvedInfo, err := resolver.ResolveS3Credentials(r.Context(), cred.AccessKey)
		if err != nil {
			return nil, false
		}
		secretKey = resolvedSecret
		info = resolvedInfo
	} else if fixedAccessKey != "" && fixedSecretKey != "" {
		if cred.AccessKey != fixedAccessKey {
			return nil, false
		}
		secretKey = fixedSecretKey
	} else {
		return nil, false
	}

	expected := computeSignature(r, cred, signedHeaders, secretKey, dateStr)
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return nil, false
	}

	return &authResult{secretKey: secretKey, info: info}, true
}

func parseAuthorization(auth string) (cred credentials, signedHeaders []string, signature string, ok bool) {
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		return
	}
	auth = auth[len("AWS4-HMAC-SHA256 "):]

	parts := strings.Split(auth, ",")
	if len(parts) < 3 {
		return
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "Credential="):
			credStr := strings.TrimPrefix(part, "Credential=")
			credParts := strings.SplitN(credStr, "/", 5)
			if len(credParts) != 5 {
				return
			}
			cred = credentials{
				AccessKey: credParts[0],
				Region:    credParts[2],
				Service:   credParts[3],
				Request:   credParts[4],
			}
		case strings.HasPrefix(part, "SignedHeaders="):
			h := strings.TrimPrefix(part, "SignedHeaders=")
			signedHeaders = strings.Split(h, ";")
		case strings.HasPrefix(part, "Signature="):
			signature = strings.TrimPrefix(part, "Signature=")
		}
	}

	ok = cred.AccessKey != "" && len(signedHeaders) > 0 && signature != ""
	return
}

func computeSignature(r *http.Request, cred credentials, signedHeaders []string, secretKey, dateStr string) string {
	credentialScope := dateStr[:8] + "/" + cred.Region + "/" + cred.Service + "/aws4_request"

	canonicalRequest := buildCanonicalRequest(r, signedHeaders)
	stringToSign := "AWS4-HMAC-SHA256\n" + dateStr + "\n" + credentialScope + "\n" + hexSHA256(canonicalRequest)

	signingKey := deriveSigningKey(secretKey, dateStr[:8], cred.Region, cred.Service)
	return hexHMACSHA256(signingKey, stringToSign)
}

func buildCanonicalRequest(r *http.Request, signedHeaders []string) string {
	canonicalURI := canonicalizeURI(r.URL.Path)
	canonicalQueryString := canonicalizeQuery(r.URL.Query())

	headers := make(map[string]string)
	for _, h := range signedHeaders {
		lh := strings.ToLower(h)
		if lh == "host" {
			headers[lh] = r.Host
		} else {
			headers[lh] = canonicalizeHeaderValue(r.Header.Values(h))
		}
	}

	canonicalHeaders := ""
	normalizedSignedHeaders := make([]string, len(signedHeaders))
	for i, h := range signedHeaders {
		normalizedSignedHeaders[i] = strings.ToLower(h)
	}
	sort.Strings(normalizedSignedHeaders)
	for _, h := range normalizedSignedHeaders {
		lh := strings.ToLower(h)
		canonicalHeaders += lh + ":" + strings.TrimSpace(headers[lh]) + "\n"
	}

	signedHeadersStr := strings.Join(normalizedSignedHeaders, ";")

	payloadHash := r.Header.Get(amzContentSHA256)
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}

	return r.Method + "\n" + canonicalURI + "\n" + canonicalQueryString + "\n" + canonicalHeaders + "\n" + signedHeadersStr + "\n" + payloadHash
}

func canonicalizeURI(path string) string {
	if path == "" {
		return "/"
	}
	return awsPercentEncode(path, false)
}

func canonicalizeQuery(values url.Values) string {
	type pair struct {
		key   string
		value string
	}

	pairs := make([]pair, 0, len(values))
	for key, list := range values {
		encodedKey := awsPercentEncode(key, true)
		if len(list) == 0 {
			pairs = append(pairs, pair{key: encodedKey})
			continue
		}
		for _, value := range list {
			pairs = append(pairs, pair{
				key:   encodedKey,
				value: awsPercentEncode(value, true),
			})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})

	var b strings.Builder
	for i, item := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(item.key)
		b.WriteByte('=')
		b.WriteString(item.value)
	}
	return b.String()
}

func canonicalizeHeaderValue(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.Join(strings.Fields(value), " "); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, ",")
}

func awsPercentEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for _, r := range s {
		if isAWSUnreserved(r) || (!encodeSlash && r == '/') {
			b.WriteRune(r)
			continue
		}
		buf := make([]byte, 4)
		n := utf8.EncodeRune(buf, r)
		for _, c := range buf[:n] {
			b.WriteByte('%')
			if c < 16 {
				b.WriteByte('0')
			}
			b.WriteString(strings.ToUpper(strconv.FormatUint(uint64(c), 16)))
		}
	}
	return b.String()
}

func isAWSUnreserved(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '-' || r == '.' || r == '_' || r == '~'
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hexHMACSHA256(key []byte, data string) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

func hexSHA256(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}
