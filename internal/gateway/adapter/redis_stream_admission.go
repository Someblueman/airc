package adapter

import (
	"context"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

// admitRedisStream attributes one entry's plain request to the route principal.
// The entry ID is broker bookkeeping only: the request's own request_id is the
// journal identity, so a redelivered or re-added entry is deduplicated there.
func admitRedisStream(parent context.Context, d *dispatch.Dispatcher, broker config.RedisStreams, stream string, entry rediswire.StreamEntry, requests chan struct{}) redisAdmission {
	r := broker.Route
	if stream != r.Stream || !config.RedisChannel(r.ResultStream) || stream == r.ResultStream || entry.Deleted || !rediswire.StreamEntryID(entry.ID) {
		return redisAdmission{refused: true}
	}
	return admitRedisRequest(parent, d, r.Principal, r.Target, r.Operations, entry.Payload, requests)
}
