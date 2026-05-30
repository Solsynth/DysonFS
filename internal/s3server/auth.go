package s3server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
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
	canonicalURI := r.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalQueryString := r.URL.RawQuery

	headers := make(map[string]string)
	for _, h := range signedHeaders {
		lh := strings.ToLower(h)
		if lh == "host" {
			headers[lh] = r.Host
		} else {
			headers[lh] = r.Header.Get(h)
		}
	}

	canonicalHeaders := ""
	sort.Strings(signedHeaders)
	for _, h := range signedHeaders {
		lh := strings.ToLower(h)
		canonicalHeaders += lh + ":" + strings.TrimSpace(headers[lh]) + "\n"
	}

	signedHeadersStr := strings.Join(signedHeaders, ";")

	payloadHash := r.Header.Get(amzContentSHA256)
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}

	return r.Method + "\n" + canonicalURI + "\n" + canonicalQueryString + "\n" + canonicalHeaders + "\n" + signedHeadersStr + "\n" + payloadHash
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
