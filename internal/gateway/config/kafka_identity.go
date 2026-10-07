package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
)

// KafkaRecordID identifies a broker-assigned record, independently of the
// producer's signed request ID. Namespace identifies one logical topic history;
// recreating that history requires a new namespace.
func KafkaRecordID(namespace, topic string, partition int32, offset int64) (string, error) {
	if !id(namespace) || !KafkaTopic(topic) || partition < 0 || offset < 0 {
		return "", errors.New("invalid Kafka record identity")
	}
	sum := sha256.Sum256([]byte("kafka-record\x00" + namespace + "\x00" + topic + "\x00" + strconv.FormatInt(int64(partition), 10) + "\x00" + strconv.FormatInt(offset, 10)))
	return hex.EncodeToString(sum[:]), nil
}

// KafkaTopic accepts Kafka's literal ASCII topic naming profile.
func KafkaTopic(topic string) bool {
	if len(topic) == 0 || len(topic) > 249 || topic == "." || topic == ".." {
		return false
	}
	for _, r := range topic {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
