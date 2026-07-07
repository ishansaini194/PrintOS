-- 0005_razorpay.sql — real Razorpay payments and refunds.
-- A payment row is now created at order time (status 'created'), verified to
-- 'paid', and moved to 'refunded' once Razorpay confirms the refund.
ALTER TABLE payments ADD COLUMN razorpay_order_id TEXT;

ALTER TABLE payments ADD COLUMN razorpay_payment_id TEXT;

-- One payment row per Razorpay order, for verification + reconciliation.
CREATE UNIQUE INDEX idx_payments_rzp_order ON payments (razorpay_order_id)
WHERE
    razorpay_order_id IS NOT NULL;

-- Record what Razorpay said about each refund.
ALTER TABLE refunds ADD COLUMN gateway_refund_id TEXT;

ALTER TABLE refunds ADD COLUMN status TEXT;
