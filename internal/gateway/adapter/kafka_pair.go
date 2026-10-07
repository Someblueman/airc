package adapter

import (
	"context"
	"crypto/tls"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/config"
)

// kafkaPair owns at most two serial sockets; colocated leaders share one.
type kafkaPair struct {
	request, result *kafkaConn
	leaders         kafkaLeaders
	once            sync.Once
}

func (p *kafkaPair) close() {
	p.once.Do(func() {
		if p.request != nil {
			p.request.close()
		}
		if p.result != nil && p.result != p.request {
			p.result.close()
		}
	})
}

func openKafkaPair(ctx context.Context, b config.Kafka, security map[int32]*tls.Config, password []byte, slots chan struct{}, seed int) (*kafkaPair, error) {
	if seed < 0 || seed >= len(b.Brokers) {
		return nil, errKafkaConnection
	}
	bootstrap := b.Brokers[seed]
	conn, err := openKafka(ctx, bootstrap, security[bootstrap.ID], b.Credential, password, slots)
	if err != nil {
		return nil, err
	}
	leaders, err := conn.metadata(b)
	if err != nil {
		conn.close()
		return nil, err
	}
	p := &kafkaPair{leaders: leaders}
	if bootstrap.ID == leaders.Request.Broker.ID {
		p.request = conn
	} else {
		conn.close()
		p.request, err = openKafka(ctx, leaders.Request.Broker, security[leaders.Request.Broker.ID], b.Credential, password, slots)
		if err == nil {
			var current kafkaLeaders
			current, err = p.request.metadata(b)
			if err == nil && current != leaders {
				err = errKafkaConnection
			}
		}
	}
	if err == nil {
		if leaders.Result.Broker.ID == leaders.Request.Broker.ID {
			p.result = p.request
		} else {
			p.result, err = openKafka(ctx, leaders.Result.Broker, security[leaders.Result.Broker.ID], b.Credential, password, slots)
			if err == nil {
				var current kafkaLeaders
				current, err = p.result.metadata(b)
				if err == nil && current != leaders {
					err = errKafkaConnection
				}
			}
		}
	}
	if err != nil || ctx.Err() != nil || p.request.ctx.Err() != nil || p.result.ctx.Err() != nil {
		p.close()
		return nil, errKafkaConnection
	}
	return p, nil
}
