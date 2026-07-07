package api

import (
	"encoding/json"
	"log"
	"time"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// StartExpirySweeper runs the hold-expiry + refund loop until stop is closed.
func StartExpirySweeper(jobs Jobs, pay Gateway, stop <-chan struct{}) {
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
				if err := SweepRefunds(jobs, pay); err != nil {
					log.Printf("sweep refunds: %v", err)
				}
			case <-stop:
				return
			}
		}
	}()
}

// SweepExpiredJobs expires due jobs, then asks connected agents to drop their
// local held copies. Offline agents are not fatal: the cloud remains the
// source of truth, and stale local holds are guarded by agent-side expiry
// checks. Refunds for the expired jobs are issued by SweepRefunds.
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

// SweepRefunds issues a real Razorpay refund for every captured payment whose
// job expired or failed, recording the gateway's refund id/status. A payment
// only leaves the refundable list once MarkRefunded flips it to 'refunded',
// so a failed gateway call is logged and retried on the next sweep — a job is
// never marked refunded on a lie.
func SweepRefunds(jobs Jobs, pay Gateway) error {
	due, err := jobs.RefundablePayments()
	if err != nil {
		return err
	}
	for _, p := range due {
		if p.RazorpayPaymentID == "" {
			// Paid without a gateway payment id should be impossible in the
			// Razorpay flow; flag it loudly rather than skipping silently.
			log.Printf("refund payment %s (job %s): no razorpay payment id — needs manual review", p.PaymentID, p.JobID)
			continue
		}
		refundID, status, err := pay.RefundPayment(p.RazorpayPaymentID, p.AmountPaise)
		if err != nil {
			log.Printf("refund payment %s (job %s): %v — will retry", p.PaymentID, p.JobID, err)
			continue
		}
		if err := jobs.MarkRefunded(p.PaymentID, p.Reason(), refundID, status, p.AmountPaise); err != nil {
			// The gateway refunded but we failed to record it. Razorpay rejects
			// a second full refund, so the retry will error and keep this loud
			// in the logs until reconciled.
			log.Printf("record refund %s for payment %s: %v", refundID, p.PaymentID, err)
			continue
		}
		log.Printf("refunded payment %s (job %s, %s): razorpay refund %s status %s",
			p.PaymentID, p.JobID, p.Reason(), refundID, status)
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
