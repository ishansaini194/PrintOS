package api

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// PayConfirm is the v1 payment STUB: it marks a job paid and pushes it to the
// shop's agent, which HOLDS it until the claim code is typed. A real gateway
// (Razorpay) later replaces only this endpoint — the rest of the flow stays.
func (h *Handlers) PayConfirm(c *fiber.Ctx) error {
	var body struct {
		JobID string `json:"job_id"`
	}
	if err := c.BodyParser(&body); err != nil || body.JobID == "" {
		return badRequest(c, "job_id required")
	}

	job, err := h.jobs.Get(body.JobID)
	if errors.Is(err, store.ErrNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "job not found"})
	}
	if err != nil {
		return serverError(c, "could not load job")
	}

	switch job.State {
	case store.JobAwaitingPayment:
		job, err = h.jobs.MarkPaid(job.ID, time.Now().Add(holdTTL()).UTC())
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "job is not payable (state changed)",
			})
		}
		if err != nil {
			return serverError(c, "could not mark job paid")
		}
	case store.JobPaid, store.JobHeld:
		// Already paid: re-confirming is idempotent — re-push below; the agent
		// dedupes on the idempotency key and will simply re-ack.
	default:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "job is not payable (state: " + job.State + ")",
		})
	}

	if err := pushJobToAgent(job); err != nil {
		// Shop offline: the job stays 'paid'; re-confirming later re-pushes it.
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"job_id": job.ID,
			"state":  job.State,
			"note":   "shop offline, will retry",
		})
	}

	return c.JSON(fiber.Map{"job_id": job.ID, "state": job.State})
}

// pushJobToAgent rebuilds the wire job from the stored row and pushes it to the
// shop's agent over the existing WebSocket. Mode is release: the agent holds it.
func pushJobToAgent(job store.Job) error {
	pj := protocol.Job{
		ID:             job.ID,
		Type:           job.Type,
		ShopID:         job.ShopID,
		IdempotencyKey: job.IdempotencyKey,
		Mode:           protocol.ModeRelease,
		ClaimCode:      job.ClaimCode,
		PDFURL:         publicURL() + "/jobs/" + job.ID + "/pdf",
		PDFSHA256:      job.PDFSHA256,
		Settings: protocol.PrintSettings{
			Color:     protocol.ColorMode(job.Type),
			Copies:    job.Copies,
			Duplex:    job.Duplex,
			PaperSize: job.PaperSize,
		},
		CreatedAt: job.CreatedAt.UTC(),
		ExpiresAt: job.ExpiresAt.UTC(),
	}
	payload, err := json.Marshal(protocol.JobPushMsg{Job: pj})
	if err != nil {
		return err
	}
	return PushToAgent(job.ShopID, protocol.Envelope{
		Type:            protocol.MsgJobPush,
		ProtocolVersion: protocol.Version,
		SentAt:          time.Now().UTC(),
		Payload:         payload,
	})
}
