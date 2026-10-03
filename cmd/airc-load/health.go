package main

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"time"
)

// One lifetime record per client; this workload never reconnects. Snapshots are
// values, so a reporting goroutine cannot race subsequent client observations.
type connectionHealth struct {
	Agent                   int       `json:"agent"`
	Connected               bool      `json:"connected"`
	AliveAtMeasurementStart bool      `json:"alive_at_measurement_start"`
	AliveAtEnd              bool      `json:"alive_at_end"`
	CloseReason             string    `json:"close_reason,omitempty"`
	ClosePhase              string    `json:"close_phase,omitempty"`
	ClosedAt                time.Time `json:"closed_at,omitzero"`
	DuringOperation         bool      `json:"during_operation"`
	ServerErrors            int       `json:"server_error_notices"`
	LastServerError         string    `json:"last_server_error,omitempty"`
}

type healthSummary struct {
	LossFraction            *float64           `json:"peer_or_transport_loss_fraction"`
	Connected               int                `json:"connected"`
	AliveAtMeasurementStart int                `json:"alive_at_measurement_start"`
	AliveAtEnd              int                `json:"alive_at_end"`
	PeerDisconnects         int                `json:"peer_disconnects"`
	TransportFailures       int                `json:"transport_failures"`
	TimeoutCloses           int                `json:"timeout_closes"`
	ClientErrorCloses       int                `json:"client_error_closes"`
	CancelledCloses         int                `json:"cancelled_closes"`
	IdleCloses              int                `json:"closes_observed_while_idle"`
	ServerErrors            int                `json:"server_error_notices"`
	Records                 []connectionHealth `json:"clients"`
}

func summarizeHealth(records []connectionHealth) healthSummary {
	slices.SortFunc(records, func(a, b connectionHealth) int { return a.Agent - b.Agent })
	r := healthSummary{Records: records}
	for _, h := range records {
		if h.Connected {
			r.Connected++
		}
		if h.AliveAtMeasurementStart {
			r.AliveAtMeasurementStart++
		}
		if h.AliveAtEnd {
			r.AliveAtEnd++
		}
		if h.CloseReason != "" && !h.DuringOperation {
			r.IdleCloses++
		}
		r.ServerErrors += h.ServerErrors
		switch h.CloseReason {
		case "peer_disconnect":
			r.PeerDisconnects++
		case "transport_failure":
			r.TransportFailures++
		case "timeout_close":
			r.TimeoutCloses++
		case "client_error_close":
			r.ClientErrorCloses++
		case "cancelled":
			r.CancelledCloses++
		}
	}
	if r.Connected > 0 {
		rate := float64(r.PeerDisconnects+r.TransportFailures) / float64(r.Connected)
		r.LossFraction = &rate
	}
	return r
}

func (a *agent) healthSnapshot() connectionHealth {
	h := a.health
	h.Agent = a.index
	h.AliveAtEnd = a.alive
	return h
}

func (a *agent) closed(ctx context.Context, reason string) {
	if !a.alive {
		return
	}
	a.alive = false
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		reason = "timeout_close"
	} else if ctx.Err() != nil {
		reason = "cancelled"
	}
	a.health.CloseReason, a.health.ClosePhase = reason, a.phaseName
	a.health.ClosedAt, a.health.DuringOperation = time.Now().UTC(), a.operating
}

func (a *agent) failed(ctx context.Context, err error) {
	reason := "client_error_close"
	var transport net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		reason = "timeout_close"
	case errors.Is(err, context.Canceled):
		reason = "cancelled"
	case errors.Is(err, io.EOF):
		reason = "peer_disconnect"
	case errors.As(err, &transport), errors.Is(err, net.ErrClosed):
		reason = "transport_failure"
	}
	a.closed(ctx, reason)
	_ = a.client.Close()
}
