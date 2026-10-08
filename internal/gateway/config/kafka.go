package config

import (
	"errors"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// Kafka owns a fixed partition. Namespace identifies logical record history;
// ClusterID and Brokers constrain which authenticated cluster may serve it.
type Kafka struct {
	ID         string          `json:"id"`
	Enabled    bool            `json:"enabled"`
	Namespace  string          `json:"namespace"`
	ClusterID  string          `json:"cluster_id"`
	Brokers    []KafkaBroker   `json:"brokers"`
	Credential KafkaCredential `json:"credential"`
	Route      KafkaRoute      `json:"route"`
}
type KafkaBroker struct {
	ID            int32  `json:"id"`
	Address       string `json:"address"`
	TLSServerName string `json:"tls_server_name"`
	TLSCAFile     string `json:"tls_ca_file,omitempty"`
}
type KafkaCredential struct {
	Username     string `json:"username"`
	PasswordFile string `json:"password_file"`
}
type KafkaRoute struct {
	Principal       string           `json:"principal"`
	Topic           string           `json:"topic"`
	Partition       int32            `json:"partition"`
	InitialOffset   *int64           `json:"initial_offset"`
	ResultTopic     string           `json:"result_topic"`
	ResultPartition int32            `json:"result_partition"`
	Target          core.Target      `json:"target"`
	Operations      []core.Operation `json:"operations"`
}

func (c Config) validateKafka() error {
	if len(c.Kafka) > 32 {
		return errors.New("Kafka owner count limit exceeded")
	}
	ids, namespaces, endpoints := map[string]bool{}, map[string]string{}, map[string]string{}
	clusters := map[string]string{}
	roles := map[string]bool{}
	for _, b := range c.Kafka {
		if !id(b.ID) || ids[b.ID] || !id(b.Namespace) || !redisText(b.ClusterID, 255) {
			return errors.New("invalid or duplicate Kafka owner")
		}
		ids[b.ID] = true
		if prior, ok := namespaces[b.Namespace]; ok && prior != b.ClusterID {
			return errors.New("Kafka namespace spans clusters")
		}
		namespaces[b.Namespace] = b.ClusterID
		if !redisText(b.Credential.Username, 255) || !absolute(b.Credential.PasswordFile) {
			return errors.New("Kafka requires SASL credentials")
		}
		profile, err := kafkaBrokers(b.Brokers, endpoints, b.ClusterID)
		if err != nil {
			return err
		}
		if prior, ok := clusters[b.ClusterID]; ok && prior != profile {
			return errors.New("Kafka cluster has inconsistent broker allowlists")
		}
		clusters[b.ClusterID] = profile
		r := b.Route
		if !id(r.Principal) || !KafkaTopic(r.Topic) || !KafkaTopic(r.ResultTopic) || r.Topic == r.ResultTopic || r.Partition < 0 || r.ResultPartition < 0 || r.InitialOffset == nil || *r.InitialOffset < 0 || *r.InitialOffset == math.MaxInt64 || len(r.Operations) == 0 || operations(r.Operations) != nil || c.ResponseSigningKey == nil {
			return errors.New("Kafka requires a fixed signed partition route and explicit initial position")
		}
		for _, part := range []struct {
			topic string
			index int32
		}{{r.Topic, r.Partition}, {r.ResultTopic, r.ResultPartition}} {
			// Partition ownership is scoped to the physical cluster, including owners
			// that choose different logical history namespaces.
			key := b.ClusterID + "\x00" + part.topic + "\x00" + strconv.FormatInt(int64(part.index), 10)
			if roles[key] {
				return errors.New("Kafka partition roles collide")
			}
			roles[key] = true
		}
		authorized := false
		for _, p := range c.Principals {
			if p.ID != r.Principal || p.Revoked || len(p.SigningKeys) == 0 || !slices.Contains(p.Targets, r.Target) {
				continue
			}
			authorized = true
			for _, op := range r.Operations {
				if !redisOperation(op) || !slices.Contains(p.Operations, op) || (op == core.HistoryRead || op == core.Search) && c.CursorKeyFile == "" {
					return errors.New("Kafka route exceeds signed authority or lacks cursor key")
				}
			}
			break
		}
		if !authorized {
			return errors.New("Kafka route requires an active signing principal")
		}
	}
	return nil
}

func kafkaBrokers(brokers []KafkaBroker, endpoints map[string]string, cluster string) (string, error) {
	if len(brokers) < 1 || len(brokers) > 32 {
		return "", errors.New("Kafka requires 1-32 allowlisted brokers")
	}
	ids, addresses := map[int32]bool{}, map[string]bool{}
	profiles := make([]string, 0, len(brokers))
	for _, b := range brokers {
		if b.ID < 0 || ids[b.ID] || endpoint(b.Address, true) != nil || !redisServerName(b.TLSServerName) || b.TLSCAFile != "" && !absolute(b.TLSCAFile) {
			return "", errors.New("invalid or duplicate Kafka broker")
		}
		ids[b.ID] = true
		host, port, _ := net.SplitHostPort(b.Address)
		ip := net.ParseIP(host)
		n, _ := strconv.Atoi(port)
		if ip.IsUnspecified() || n == 0 {
			return "", errors.New("Kafka requires a numeric dial endpoint")
		}
		address := net.JoinHostPort(ip.String(), strconv.Itoa(n))
		if addresses[address] {
			return "", errors.New("duplicate Kafka broker endpoint")
		}
		addresses[address] = true
		if prior, ok := endpoints[address]; ok && prior != cluster {
			return "", errors.New("Kafka endpoint spans clusters")
		}
		endpoints[address] = cluster
		profiles = append(profiles, strconv.FormatInt(int64(b.ID), 10)+"\x00"+address+"\x00"+b.TLSServerName+"\x00"+b.TLSCAFile)
	}
	slices.Sort(profiles)
	return strings.Join(profiles, "\x00\x00"), nil
}
