package api

import (
	"encoding/json"
	"log"
	"time"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// StartExpirySweeper runs the hold-expiry loop until stop is closed.
func StartExpirySweeper(jobs Jobs, stop <-chan struct{}) {
	interval := expirySweepInterval()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := SweepExpiredJobs(jobs, time.Now().UTC()); err != nil {
					log.Printf("expire held jobs: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()
}

// SweepExpiredJobs expires/refunds due jobs, then asks connected agents to drop
// their local held copies. Offline agents are not fatal: the cloud remains the
// source of truth, and stale local holds are guarded by agent-side expiry checks.
func SweepExpiredJobs(jobs Jobs, now time.Time) error {
	expired, err := jobs.ExpireDue(now)
	if err != nil {
		return err
	}
	for _, job := range expired {
		if err := sendCancel(job); err != nil {
			log.Printf("cancel expired job %s for shop %s: %v", job.ID, job.ShopID, err)
		}
	}
	return nil
}

func sendCancel(job store.ExpiredJob) error {
	payload, err := json.Marshal(protocol.CancelMsg{JobID: job.ID})
	if err != nil {
		return err
	}
	return sendToAgent(job.ShopID, protocol.Envelope{
		Type:            protocol.MsgCancel,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	})
}
