package adapter

import (
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/sqswire"
)

// MaxEventBridgeBytes bounds the EventBridge envelope carried by one SQS message.
const MaxEventBridgeBytes = 32 << 10

type eventBridgeEnvelope struct {
	Version    string            `json:"version"`
	ID         string            `json:"id"`
	Source     string            `json:"source"`
	DetailType string            `json:"detail-type"`
	Account    string            `json:"account"`
	Time       string            `json:"time"`
	Region     string            `json:"region"`
	Resources  []string          `json:"resources"`
	Detail     eventBridgeDetail `json:"detail"`
	ReplayName *string           `json:"replay-name,omitempty"`
}

type eventBridgeDetail struct {
	AIRCJWE string `json:"airc_jwe"`
}

type SQSResultEventBridge struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	DetailType string `json:"detail_type"`
	Account    string `json:"account"`
	Region     string `json:"region"`
}

func parseEventBridgeMessage(body []byte, b config.SQS) (string, *SQSResultEventBridge, error) {
	if b.EventBridge == nil {
		return "", nil, errors.New("invalid EventBridge envelope")
	}
	var event eventBridgeEnvelope
	if err := jsoninput.DecodeDepth(body, &event, MaxEventBridgeBytes, 8); err != nil {
		return "", nil, errors.New("invalid EventBridge envelope")
	}
	account, _, err := sqswire.ParseQueueURL(b.Region, b.RequestQueueURL)
	if err != nil || event.Version != "0" || !eventBridgeUUID(event.ID) || event.Source != b.EventBridge.Source || event.DetailType != b.EventBridge.DetailType || event.Account != account || event.Region != b.Region || len(event.Resources) > 32 || event.Resources == nil {
		return "", nil, errors.New("invalid EventBridge metadata")
	}
	if _, err := time.Parse(time.RFC3339Nano, event.Time); err != nil {
		return "", nil, errors.New("invalid EventBridge time")
	}
	for _, resource := range event.Resources {
		if !eventBridgeASCII(resource, 1, 512) {
			return "", nil, errors.New("invalid EventBridge resource")
		}
	}
	if event.ReplayName != nil && !eventBridgeASCII(*event.ReplayName, 1, 64) {
		return "", nil, errors.New("invalid EventBridge replay name")
	}
	token := event.Detail.AIRCJWE
	if !eventBridgeCompactJWE(token) {
		return "", nil, errors.New("invalid EventBridge detail")
	}
	return token, &SQSResultEventBridge{ID: event.ID, Source: event.Source, DetailType: event.DetailType, Account: event.Account, Region: event.Region}, nil
}

func eventBridgeUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

func eventBridgeASCII(value string, minBytes, maxBytes int) bool {
	if len(value) < minBytes || len(value) > maxBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func eventBridgeCompactJWE(value string) bool {
	if len(value) == 0 || len(value) > MaxRequestFrameBytes {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 5 {
		return false
	}
	for _, part := range parts {
		for i := 0; i < len(part); i++ {
			b := part[i]
			if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
				return false
			}
		}
	}
	return true
}
