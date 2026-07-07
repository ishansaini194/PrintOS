package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// uniqueOrderID mimics Razorpay's globally-unique order ids — the payments
// table enforces uniqueness on razorpay_order_id and the test DB persists
// across runs, so fixed ids would collide with rows from earlier runs.
func uniqueOrderID(tag string) string {
	return fmt.Sprintf("order_%s_%d", tag, time.Now().UnixNano())
}

func TestPaymentOrderLifecycle(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)
	job := makeJob(t, js, shopID, "700001")
	orderA, orderB, orderC := uniqueOrderID("A"), uniqueOrderID("B"), uniqueOrderID("C")

	if _, err := js.PaymentByJob(job.ID); !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("PaymentByJob before order: err = %v, want ErrPaymentNotFound", err)
	}

	if err := js.SavePaymentOrder(job.ID, job.AmountPaise, orderA); err != nil {
		t.Fatalf("SavePaymentOrder: %v", err)
	}
	p, err := js.PaymentByJob(job.ID)
	if err != nil || p.Status != PaymentCreated || p.RazorpayOrderID != orderA || p.AmountPaise != job.AmountPaise {
		t.Fatalf("payment = %+v (%v), want created/order_A/%d", p, err, job.AmountPaise)
	}

	// While still unpaid, a retried order may replace the order id.
	if err := js.SavePaymentOrder(job.ID, job.AmountPaise, orderB); err != nil {
		t.Fatalf("SavePaymentOrder retry: %v", err)
	}
	if p, _ = js.PaymentByJob(job.ID); p.RazorpayOrderID != orderB {
		t.Fatalf("order id = %q, want order_B after retry", p.RazorpayOrderID)
	}

	// MarkPaid flips job + payment together and stamps the razorpay payment id.
	expires := time.Now().Add(30 * time.Minute).UTC()
	paidJob, err := js.MarkPaid(job.ID, "pay_X", expires)
	if err != nil || paidJob.State != JobPaid {
		t.Fatalf("MarkPaid = %+v, %v", paidJob, err)
	}
	p, _ = js.PaymentByJob(job.ID)
	if p.Status != PaymentPaid || p.RazorpayPaymentID != "pay_X" {
		t.Fatalf("payment after MarkPaid = %+v, want paid/pay_X", p)
	}

	// Once paid, a stray order save must not touch the row.
	if err := js.SavePaymentOrder(job.ID, job.AmountPaise, orderC); err != nil {
		t.Fatalf("SavePaymentOrder after paid: %v", err)
	}
	if p, _ = js.PaymentByJob(job.ID); p.RazorpayOrderID != orderB || p.Status != PaymentPaid {
		t.Fatalf("payment mutated after paid: %+v", p)
	}

	// Re-paying is refused (job already left awaiting_payment).
	if _, err := js.MarkPaid(job.ID, "pay_Y", expires); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second MarkPaid err = %v, want ErrNotFound", err)
	}
}

func TestRefundableAndMarkRefunded(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)

	pay := func(claim, rzpPay string) Job {
		job := makeJob(t, js, shopID, claim)
		if err := js.SavePaymentOrder(job.ID, job.AmountPaise, uniqueOrderID(claim)); err != nil {
			t.Fatal(err)
		}
		if _, err := js.MarkPaid(job.ID, rzpPay, time.Now().Add(time.Hour).UTC()); err != nil {
			t.Fatal(err)
		}
		return job
	}

	expired := pay("700002", "pay_exp")
	failed := pay("700003", "pay_fail")
	healthy := pay("700004", "pay_ok") // stays paid — must not be refundable

	if err := js.SetState(expired.ID, JobExpired); err != nil {
		t.Fatal(err)
	}
	if err := js.SetState(failed.ID, "failed"); err != nil {
		t.Fatal(err)
	}

	due, err := js.RefundablePayments()
	if err != nil {
		t.Fatalf("RefundablePayments: %v", err)
	}
	byJob := map[string]RefundablePayment{}
	for _, r := range due {
		byJob[r.JobID] = r
	}
	if _, ok := byJob[healthy.ID]; ok {
		t.Error("healthy paid job listed as refundable")
	}
	exp, ok := byJob[expired.ID]
	if !ok || exp.RazorpayPaymentID != "pay_exp" || exp.Reason() != RefundReasonHoldExpired {
		t.Fatalf("expired refundable = %+v (ok=%v), want pay_exp/hold_expired", exp, ok)
	}
	fl, ok := byJob[failed.ID]
	if !ok || fl.Reason() != RefundReasonPrintFailed {
		t.Fatalf("failed refundable = %+v (ok=%v), want print_failed", fl, ok)
	}

	// Record the refund; the payment leaves the refundable list.
	if err := js.MarkRefunded(exp.PaymentID, exp.Reason(), "rfnd_1", "processed", exp.AmountPaise); err != nil {
		t.Fatalf("MarkRefunded: %v", err)
	}
	// Idempotent: recording again is a no-op, not an error.
	if err := js.MarkRefunded(exp.PaymentID, exp.Reason(), "rfnd_dup", "processed", exp.AmountPaise); err != nil {
		t.Fatalf("MarkRefunded repeat: %v", err)
	}
	due, err = js.RefundablePayments()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range due {
		if r.JobID == expired.ID {
			t.Fatal("refunded payment still listed as refundable")
		}
	}

	var count int64
	if err := js.db.Raw(`SELECT COUNT(*) FROM refunds WHERE payment_id = ?`, exp.PaymentID).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("refund rows = %d, want exactly 1", count)
	}
	var gwID string
	if err := js.db.Raw(`SELECT gateway_refund_id FROM refunds WHERE payment_id = ?`, exp.PaymentID).Scan(&gwID).Error; err != nil {
		t.Fatal(err)
	}
	if gwID != "rfnd_1" {
		t.Fatalf("gateway_refund_id = %q, want rfnd_1", gwID)
	}
}

func TestExpireDueDoesNotTouchPayments(t *testing.T) {
	js := testDB(t)
	shopID := makeShop(t, js)
	job := makeJob(t, js, shopID, "700005")
	if err := js.SavePaymentOrder(job.ID, job.AmountPaise, uniqueOrderID("e")); err != nil {
		t.Fatal(err)
	}
	if _, err := js.MarkPaid(job.ID, "pay_e", time.Now().Add(-time.Minute).UTC()); err != nil {
		t.Fatal(err)
	}

	expired, err := js.ExpireDue(time.Now().UTC())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	found := false
	for _, e := range expired {
		if e.ID == job.ID {
			found = true
			if e.ShopID != shopID {
				t.Errorf("shop id = %q, want %q", e.ShopID, shopID)
			}
		}
	}
	if !found {
		t.Fatal("due job not returned by ExpireDue")
	}

	// The payment stays 'paid' — refunds are the sweep's job, not ExpireDue's.
	p, err := js.PaymentByJob(job.ID)
	if err != nil || p.Status != PaymentPaid {
		t.Fatalf("payment = %+v (%v), want still paid", p, err)
	}

	// Idempotent: a second pass returns nothing for this job.
	expired, err = js.ExpireDue(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range expired {
		if e.ID == job.ID {
			t.Fatal("job expired twice")
		}
	}
}
