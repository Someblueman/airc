package s3wire

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func payloadHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func signRequest(request *http.Request, region, access string, secret []byte, now time.Time) error {
	if request == nil || request.URL == nil || request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" || len(secret) == 0 {
		return ErrRequest
	}
	dateTime := now.UTC().Format("20060102T150405Z")
	date := now.UTC().Format("20060102")
	request.Header.Set("X-Amz-Date", dateTime)

	headers := map[string]string{"host": request.Host}
	for _, name := range []string{"content-type", "if-match", "if-none-match", "x-amz-content-sha256", "x-amz-date", "x-amz-expected-bucket-owner", "x-amz-security-token"} {
		values := request.Header.Values(http.CanonicalHeaderKey(name))
		if len(values) == 0 {
			continue
		}
		cleaned := make([]string, len(values))
		for i, value := range values {
			cleaned[i] = strings.Join(strings.Fields(value), " ")
		}
		headers[name] = strings.Join(cleaned, ",")
	}
	if headers["host"] == "" {
		return ErrRequest
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(headers[name])
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")
	canonicalRequest := strings.Join([]string{
		request.Method,
		request.URL.EscapedPath(),
		request.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		request.Header.Get("X-Amz-Content-Sha256"),
	}, "\n")
	canonicalSum := sha256.Sum256([]byte(canonicalRequest))
	scope := date + "/" + region + "/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + dateTime + "\n" + scope + "\n" + hex.EncodeToString(canonicalSum[:])
	initialKey := append([]byte("AWS4"), secret...)
	dateKey := hmacSHA256(initialKey, []byte(date))
	clear(initialKey)
	regionKey := hmacSHA256(dateKey, []byte(region))
	serviceKey := hmacSHA256(regionKey, []byte("s3"))
	signingKey := hmacSHA256(serviceKey, []byte("aws4_request"))
	signature := hmacSHA256(signingKey, []byte(stringToSign))
	clear(dateKey)
	clear(regionKey)
	clear(serviceKey)
	clear(signingKey)
	request.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", access, scope, signedHeaders, hex.EncodeToString(signature)))
	clear(signature)
	return nil
}

func hmacSHA256(key, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}
