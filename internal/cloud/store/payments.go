package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// Payment lifecycle: 'created' (Razorpay order made, awaiting checkout) →
// 'paid' (signature verified) → 'refunded' (Razorpay refund recorded).
const (
	PaymentCreated  = "created"
	PaymentPaid     = "paid"
	PaymentRefunded = "refunded"
)

// Refund reasons — each may refund a given payment at most once (unique index
// on payment_id + reason), and a payment is only ever refunded once in total
// (guarded by the paid → refunded status transition).
const (
	RefundReasonHoldExpired = "hold_expired"
	RefundReasonPrintFailed = "print_failed"
)

// ErrPaymentNotFound is returned when a job has no payment row yet.
var ErrPaymentNotFound = errors.New("payment not found")

// Payment mirrors a row in the payments table.
type Payment struct {
	ID                string    `gorm:"column:id"`
	JobID             string    `gorm:"column:job_id"`
	AmountPaise       int       `gorm:"column:amount_paise"`
	Status            string    `gorm:"column:status"`
	RazorpayOrderID   string    `gorm:"column:razorpay_order_id"`
	RazorpayPaymentID string    `gorm:"column:razorpay_payment_id"`
	CreatedAt         time.Time `gorm:"column:created_at"`
}

// PaymentByJob loads the payment row for a job, or ErrPaymentNotFound.
func (s *JobStore) PaymentByJob(jobID string) (Payment, error) {
	var p Payment
	res := s.db.Raw(
		`SELECT id, job_id, amount_paise, status,
		        COALESCE(razorpay_order_id, '') AS razorpay_order_id,
		        COALESCE(razorpay_payment_id, '') AS razorpay_payment_id,
		        created_at
		 FROM payments WHERE job_id = ?`, jobID,
	).Scan(&p)
	if res.Error != nil {
		return Payment{}, res.Error
	}
	if res.RowsAffected == 0 {
		return Payment{}, ErrPaymentNotFound
	}
	return p, nil
}

// SavePaymentOrder records the Razorpay order created for a job, upserting the
// payment row. A retried order overwrites the order id only while the payment
// is still 'created' — once paid, the row is immutable from this path.
func (s *JobStore) SavePaymentOrder(jobID string, amountPaise int, razorpayOrderID string) error {
	return s.db.Exec(
		`INSERT INTO payments (job_id, amount_paise, status, razorpay_order_id)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (job_id) DO UPDATE
		 SET razorpay_order_id = EXCLUDED.razorpay_order_id, updated_at = now()
		 WHERE payments.status = ?`,
		jobID, amountPaise, PaymentCreated, razorpayOrderID, PaymentCreated,
	).Error
}

// RefundablePayment is a captured payment whose job ended in a refundable
// state (expired hold or failed print) and has not been refunded yet.
type RefundablePayment struct {
	PaymentID         string `gorm:"column:payment_id"`
	JobID             string `gorm:"column:job_id"`
	JobState          string `gorm:"column:job_state"`
	RazorpayPaymentID string `gorm:"column:razorpay_payment_id"`
	AmountPaise       int    `gorm:"column:amount_paise"`
}

// Reason maps the job's terminal state to the refund reason recorded in the DB.
func (r RefundablePayment) Reason() string {
	if r.JobState == "failed" {
		return RefundReasonPrintFailed
	}
	return RefundReasonHoldExpired
}

// RefundablePayments lists paid payments whose jobs expired or failed — the
// sweeper's work queue. A payment leaves this list only when MarkRefunded
// flips it to 'refunded', so failed gateway calls are naturally retried.
func (s *JobStore) RefundablePayments() ([]RefundablePayment, error) {
	var out []RefundablePayment
	err := s.db.Raw(
		`SELECT p.id AS payment_id, p.job_id, j.state AS job_state,
		        COALESCE(p.razorpay_payment_id, '') AS razorpay_payment_id,
		        p.amount_paise
		 FROM payments p
		 JOIN jobs j ON j.id = p.job_id
		 WHERE p.status = ? AND j.state IN (?, ?)
		 ORDER BY p.created_at, p.id`,
		PaymentPaid, JobExpired, protocolStateFailed,
	).Scan(&out).Error
	return out, err
}

// protocolStateFailed mirrors protocol.StateFailed; the store package does not
// import protocol, and the agent writes this state verbatim via SetState.
const protocolStateFailed = "failed"

// MarkRefunded records a gateway-confirmed refund: payment 'paid' → 'refunded'
// plus a refund row with Razorpay's refund id/status. Idempotent — if the
// payment already left 'paid', nothing is written.
func (s *JobStore) MarkRefunded(paymentID, reason, gatewayRefundID, status string, amountPaise int) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`UPDATE payments SET status = ?, updated_at = now()
			 WHERE id = ? AND status = ?`,
			PaymentRefunded, paymentID, PaymentPaid,
		)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil // already refunded (or not paid) — nothing to record
		}
		return tx.Exec(
			`INSERT INTO refunds (payment_id, reason, amount_paise, gateway_refund_id, status)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT DO NOTHING`,
			paymentID, reason, amountPaise, gatewayRefundID, status,
		).Error
	})
}
