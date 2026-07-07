package api

import (
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ishansaini194/PrintOS/internal/cloud/store"
	"github.com/ishansaini194/PrintOS/pkg/protocol"
)

// Gateway is the payment-gateway surface the handlers and refund sweep need.
// Implemented by razorpay.Client; faked in tests. The key secret stays inside
// the implementation — handlers only ask it to verify.
type Gateway interface {
	KeyID() string
	CreateOrder(amountPaise int, receipt string) (razorpayOrderID string, err error)
	RefundPayment(razorpayPaymentID string, amountPaise int) (refundID, status string, err error)
	VerifySignature(orderID, paymentID, signature string) bool
}

// PayOrder creates (or returns the already-created) Razorpay order for a job
// awaiting payment. The UI opens checkout with the returned order id + key id.
func (h *Handlers) PayOrder(c *fiber.Ctx) error {
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
	if job.State != store.JobAwaitingPayment {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "job is not payable (state: " + job.State + ")",
		})
	}

	// Reuse an existing unpaid order — a customer re-opening checkout must not
	// mint a fresh Razorpay order for the same job.
	if p, err := h.jobs.PaymentByJob(job.ID); err == nil &&
		p.Status == store.PaymentCreated && p.RazorpayOrderID != "" {
		return c.JSON(payOrderResponse(p.RazorpayOrderID, job.AmountPaise, h.pay.KeyID()))
	}

	orderID, err := h.pay.CreateOrder(job.AmountPaise, job.ID)
	if err != nil {
		log.Printf("razorpay create order for job %s: %v", job.ID, err)
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "payment gateway unavailable"})
	}
	if err := h.jobs.SavePaymentOrder(job.ID, job.AmountPaise, orderID); err != nil {
		return serverError(c, "could not record payment order")
	}
	return c.JSON(payOrderResponse(orderID, job.AmountPaise, h.pay.KeyID()))
}

func payOrderResponse(orderID string, amountPaise int, keyID string) fiber.Map {
	return fiber.Map{
		"razorpay_order_id": orderID,
		"amount_paise":      amountPaise,
		"key_id":            keyID,
	}
}

// PayVerify is the security gate that replaces the old stub confirm: the
// browser reports checkout success, and we only believe it if the Razorpay
// signature checks out against our secret. Valid → job paid, pushed to the
// agent (held), hold TTL started. Invalid → 400 and the job stays unpaid.
func (h *Handlers) PayVerify(c *fiber.Ctx) error {
	var body struct {
		JobID             string `json:"job_id"`
		RazorpayPaymentID string `json:"razorpay_payment_id"`
		RazorpayOrderID   string `json:"razorpay_order_id"`
		RazorpaySignature string `json:"razorpay_signature"`
	}
	if err := c.BodyParser(&body); err != nil ||
		body.JobID == "" || body.RazorpayPaymentID == "" ||
		body.RazorpayOrderID == "" || body.RazorpaySignature == "" {
		return badRequest(c, "job_id, razorpay_payment_id, razorpay_order_id, razorpay_signature required")
	}

	job, err := h.jobs.Get(body.JobID)
	if errors.Is(err, store.ErrNotFound) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "job not found"})
	}
	if err != nil {
		return serverError(c, "could not load job")
	}

	// The order must be the one we created for this job — a signature that is
	// valid for some other order/payment pair must not pay this job.
	payment, err := h.jobs.PaymentByJob(job.ID)
	if errors.Is(err, store.ErrPaymentNotFound) || (err == nil && payment.RazorpayOrderID != body.RazorpayOrderID) {
		return badRequest(c, "unknown order for this job")
	}
	if err != nil {
		return serverError(c, "could not load payment")
	}
	if !h.pay.VerifySignature(body.RazorpayOrderID, body.RazorpayPaymentID, body.RazorpaySignature) {
		log.Printf("/pay/verify: invalid signature for job %s (order %s)", job.ID, body.RazorpayOrderID)
		return badRequest(c, "invalid payment signature")
	}

	switch job.State {
	case store.JobAwaitingPayment:
		job, err = h.jobs.MarkPaid(job.ID, body.RazorpayPaymentID, time.Now().Add(holdTTL()).UTC())
		if errors.Is(err, store.ErrNotFound) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "job is not payable (state changed)",
			})
		}
		if err != nil {
			return serverError(c, "could not mark job paid")
		}
	case store.JobHeld:
		// Already paid AND on the shop PC: re-verifying is a no-op success —
		// no second push.
		return c.JSON(fiber.Map{"job_id": job.ID, "state": job.State})
	case store.JobPaid:
		// Paid but never acked (shop was offline): fall through and re-push;
		// the agent dedupes on the idempotency key.
	default:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "job is not payable (state: " + job.State + ")",
		})
	}

	if err := pushJobToAgent(job); err != nil {
		// Shop offline: the job stays 'paid'; re-verifying later re-pushes it.
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
		PDFURL:         publicLink("/jobs/" + job.ID + "/pdf"),
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
