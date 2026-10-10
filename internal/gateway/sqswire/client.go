package sqswire

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const (
	maxResponseBytes = 256 << 10
	maxReceiveBody   = 16 << 10
	maxSendBody      = 64 << 10
	awsTargetPrefix  = "AmazonSQS."
)

var (
	ErrRequest    = errors.New("sqs request failed")
	ErrQueueURL   = errors.New("invalid sqs queue url")
	regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$`)
)

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey []byte
	SessionToken    []byte
}

type Client struct {
	region string
	host   string
	access string
	secret []byte
	token  []byte
	mu     sync.RWMutex
	closed bool
	http   *http.Client
	trans  *http.Transport
}

type Message struct {
	ID            string
	ReceiptHandle string
	Body          string
}

func NewClient(region string, creds Credentials) (*Client, error) {
	if !validRegion(region) || !validCredentials(creds) {
		return nil, ErrRequest
	}
	endpoint := "sqs." + region + ".amazonaws.com"
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true, ForceAttemptHTTP2: false,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: endpoint},
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 14 * time.Second,
		MaxResponseHeaderBytes: 8192, MaxConnsPerHost: 2, MaxIdleConns: 2,
		MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
		TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	return &Client{
		region: region, host: endpoint, access: creds.AccessKeyID,
		secret: bytes.Clone(creds.SecretAccessKey), token: bytes.Clone(creds.SessionToken),
		trans: transport, http: &http.Client{Transport: transport, Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func validRegion(region string) bool {
	return regionPattern.MatchString(region) &&
		!strings.HasPrefix(region, "cn-") && !strings.HasPrefix(region, "us-gov-") &&
		!strings.HasPrefix(region, "us-iso-") && !strings.HasPrefix(region, "us-isob-") &&
		!strings.HasPrefix(region, "eu-isoe-") && !strings.HasPrefix(region, "us-isof-")
}

func validCredentials(c Credentials) bool {
	if len(c.AccessKeyID) < 16 || len(c.AccessKeyID) > 128 || len(c.SecretAccessKey) < 32 || len(c.SecretAccessKey) > 128 || len(c.SessionToken) > 4096 {
		return false
	}
	for _, b := range c.AccessKeyID {
		if !(b >= 'A' && b <= 'Z' || b >= '0' && b <= '9') {
			return false
		}
	}
	return printableCredential(c.SecretAccessKey, false) && printableCredential(c.SessionToken, true)
}

func printableCredential(value []byte, emptyOK bool) bool {
	if len(value) == 0 {
		return emptyOK
	}
	for _, b := range value {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		clear(c.secret)
		clear(c.token)
		c.secret = nil
		c.token = nil
		c.trans.CloseIdleConnections()
	}
	c.mu.Unlock()
}

func ParseQueueURL(region, raw string) (account, name string, err error) {
	if !validRegion(region) || len(raw) == 0 || len(raw) > 512 {
		return "", "", ErrQueueURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "sqs."+region+".amazonaws.com" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.ForceQuery || u.Port() != "" {
		return "", "", ErrQueueURL
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) != 2 || strings.Contains(u.Path, "%") || len(parts[0]) != 12 || !digits(parts[0]) || !validQueueName(parts[1]) || raw != "https://sqs."+region+".amazonaws.com/"+parts[0]+"/"+parts[1] {
		return "", "", ErrQueueURL
	}
	return parts[0], parts[1], nil
}

func validQueueName(name string) bool {
	if len(name) == 0 || len(name) > 80 || strings.HasSuffix(name, ".fifo") {
		return false
	}
	for _, b := range name {
		if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

func digits(value string) bool {
	for _, b := range value {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func (c *Client) Receive(ctx context.Context, queueURL string) (*Message, error) {
	if ctx == nil || !c.validQueue(queueURL) {
		return nil, ErrRequest
	}
	var out struct {
		Messages []struct {
			ID      string `json:"MessageId"`
			Receipt string `json:"ReceiptHandle"`
			Body    string `json:"Body"`
			MD5     string `json:"MD5OfBody"`
		} `json:"Messages"`
	}
	request := struct {
		QueueURL            string `json:"QueueUrl"`
		MaxNumberOfMessages int    `json:"MaxNumberOfMessages"`
		WaitTimeSeconds     int    `json:"WaitTimeSeconds"`
		VisibilityTimeout   int    `json:"VisibilityTimeout"`
	}{queueURL, 1, 10, 30}
	if err := c.call(ctx, "ReceiveMessage", request, &out); err != nil {
		return nil, err
	}
	if len(out.Messages) == 0 {
		return nil, nil
	}
	if len(out.Messages) != 1 {
		return nil, ErrRequest
	}
	m := out.Messages[0]
	if !boundedToken(m.ID, 256) || !boundedToken(m.Receipt, 4096) || len(m.Body) == 0 || len(m.Body) > maxReceiveBody || !utf8.ValidString(m.Body) || !validMD5(m.MD5, m.Body) {
		return nil, ErrRequest
	}
	return &Message{ID: m.ID, ReceiptHandle: m.Receipt, Body: m.Body}, nil
}

func (c *Client) Send(ctx context.Context, queueURL, body string) error {
	if ctx == nil || !c.validQueue(queueURL) || len(body) == 0 || len(body) > maxSendBody || !utf8.ValidString(body) {
		return ErrRequest
	}
	var out struct {
		ID  string `json:"MessageId"`
		MD5 string `json:"MD5OfMessageBody"`
	}
	request := struct {
		QueueURL     string `json:"QueueUrl"`
		Body         string `json:"MessageBody"`
		DelaySeconds int    `json:"DelaySeconds"`
	}{queueURL, body, 0}
	if err := c.call(ctx, "SendMessage", request, &out); err != nil || !boundedToken(out.ID, 256) || !validMD5(out.MD5, body) {
		return ErrRequest
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, queueURL, receipt string) error {
	if ctx == nil || !c.validQueue(queueURL) || !boundedToken(receipt, 4096) {
		return ErrRequest
	}
	request := struct {
		QueueURL string `json:"QueueUrl"`
		Receipt  string `json:"ReceiptHandle"`
	}{queueURL, receipt}
	return c.call(ctx, "DeleteMessage", request, &struct{}{})
}

func (c *Client) validQueue(raw string) bool {
	if c == nil {
		return false
	}
	_, _, err := ParseQueueURL(c.region, raw)
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && err == nil
}

func (c *Client) call(ctx context.Context, operation string, request, response any) error {
	if c == nil || ctx == nil || !validRegion(c.region) || response == nil {
		return ErrRequest
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || len(c.secret) == 0 {
		return ErrRequest
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > maxSendBody+8192 {
		return ErrRequest
	}
	now := time.Now().UTC()
	date := now.Format("20060102T150405Z")
	dateScope := now.Format("20060102")
	target := awsTargetPrefix + operation
	contentType := "application/x-amz-json-1.0"
	payloadHash := sha256.Sum256(body)
	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + c.host + "\n" +
		"x-amz-date:" + date + "\n"
	signedHeaders := "content-type;host;x-amz-date"
	if len(c.token) > 0 {
		canonicalHeaders += "x-amz-security-token:" + string(c.token) + "\n"
		signedHeaders += ";x-amz-security-token"
	}
	canonicalHeaders += "x-amz-target:" + target + "\n"
	signedHeaders += ";x-amz-target"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + hex.EncodeToString(payloadHash[:])
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	scope := dateScope + "/" + c.region + "/sqs/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + date + "\n" + scope + "\n" + hex.EncodeToString(canonicalHash[:])
	awsSecret := append([]byte("AWS4"), c.secret...)
	kDate := hmacSHA256(awsSecret, dateScope)
	clear(awsSecret)
	kRegion := hmacSHA256(kDate, c.region)
	kService := hmacSHA256(kRegion, "sqs")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	clear(kDate)
	clear(kRegion)
	clear(kService)
	clear(kSigning)
	requestHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+c.host+"/", bytes.NewReader(body))
	if err != nil {
		return ErrRequest
	}
	requestHTTP.Host = c.host
	requestHTTP.Header.Set("Content-Type", contentType)
	requestHTTP.Header.Set("X-Amz-Date", date)
	requestHTTP.Header.Set("X-Amz-Target", target)
	requestHTTP.Header.Set("Accept", "application/x-amz-json-1.0")
	if len(c.token) > 0 {
		requestHTTP.Header.Set("X-Amz-Security-Token", string(c.token))
	}
	requestHTTP.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.access+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	responseHTTP, err := c.http.Do(requestHTTP)
	if err != nil {
		return ErrRequest
	}
	defer responseHTTP.Body.Close()
	mediaType, params, mediaErr := mime.ParseMediaType(responseHTTP.Header.Get("Content-Type"))
	charset := strings.ToLower(params["charset"])
	if responseHTTP.StatusCode != http.StatusOK || responseHTTP.Header.Get("Content-Encoding") != "" || mediaErr != nil || mediaType != "application/x-amz-json-1.0" || charset != "" && charset != "utf-8" {
		return ErrRequest
	}
	data, err := io.ReadAll(io.LimitReader(responseHTTP.Body, maxResponseBytes+1))
	defer clear(data)
	if err != nil || len(data) > maxResponseBytes || ctx.Err() != nil {
		return ErrRequest
	}
	// DeleteMessage's documented JSON-protocol success has no response body.
	if operation == "DeleteMessage" && len(data) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' || jsoninput.ValidateDepth(data, maxResponseBytes, 16) != nil {
		return ErrRequest
	}
	if err := json.Unmarshal(data, response); err != nil {
		return ErrRequest
	}
	return nil
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, value)
	return mac.Sum(nil)
}

func validMD5(expected, body string) bool {
	if len(expected) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || strings.ToLower(expected) != expected {
		return false
	}
	actual := md5.Sum([]byte(body))
	return hmac.Equal(decoded, actual[:])
}

func boundedToken(value string, max int) bool {
	if len(value) == 0 || len(value) > max || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}

func (c *Client) CheckQueue(ctx context.Context, queueURL string, requireRedrive bool) error {
	if c == nil || ctx == nil {
		return ErrRequest
	}
	account, name, err := ParseQueueURL(c.region, queueURL)
	if err != nil || !c.validQueue(queueURL) {
		return ErrRequest
	}
	var out struct {
		Attributes map[string]string `json:"Attributes"`
	}
	request := struct {
		QueueURL string   `json:"QueueUrl"`
		Names    []string `json:"AttributeNames"`
	}{
		queueURL, []string{"QueueArn", "FifoQueue", "RedrivePolicy", "MaximumMessageSize"},
	}
	if err := c.call(ctx, "GetQueueAttributes", request, &out); err != nil || out.Attributes == nil {
		return ErrRequest
	}
	a := out.Attributes
	if a["QueueArn"] != "arn:aws:sqs:"+c.region+":"+account+":"+name || a["FifoQueue"] != "" && a["FifoQueue"] != "false" {
		return ErrRequest
	}
	maximum, err := strconv.ParseInt(a["MaximumMessageSize"], 10, 64)
	minimum := int64(maxSendBody)
	if requireRedrive {
		minimum = maxReceiveBody
	}
	if err != nil || strconv.FormatInt(maximum, 10) != a["MaximumMessageSize"] || maximum < minimum {
		return ErrRequest
	}
	policy := a["RedrivePolicy"]
	if policy == "" {
		if requireRedrive {
			return ErrRequest
		}
		return nil
	}
	if !validRedrivePolicy(policy, c.region, account, name) {
		return ErrRequest
	}
	return nil
}

func validRedrivePolicy(raw, region, account, queueName string) bool {
	if len(raw) == 0 || len(raw) > 4096 || jsoninput.ValidateDepth([]byte(raw), 4096, 4) != nil {
		return false
	}
	var policy map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &policy) != nil || len(policy) != 2 {
		return false
	}
	var arn string
	if json.Unmarshal(policy["deadLetterTargetArn"], &arn) != nil {
		return false
	}
	countRaw := bytes.TrimSpace(policy["maxReceiveCount"])
	var count string
	if len(countRaw) == 0 {
		return false
	}
	if countRaw[0] == '"' {
		if json.Unmarshal(countRaw, &count) != nil {
			return false
		}
	} else {
		count = string(countRaw)
	}
	wantPrefix := "arn:aws:sqs:" + region + ":" + account + ":"
	if !strings.HasPrefix(arn, wantPrefix) {
		return false
	}
	name := strings.TrimPrefix(arn, wantPrefix)
	if !validQueueName(name) || name == queueName {
		return false
	}
	n, err := strconv.ParseUint(count, 10, 32)
	return err == nil && n > 0 && n <= 1000 && strconv.FormatUint(n, 10) == count
}
