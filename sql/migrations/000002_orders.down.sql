-- 000002_orders.down.sql
-- Reverting this migration fails if any order has a NULL payment_id. Delete or
-- backfill those pending orders first; inventing a payment reference would hide
-- the fact that no payment exists.

ALTER TABLE "orders" ALTER COLUMN "status" SET DEFAULT 'paid';
ALTER TABLE "orders" ALTER COLUMN "payment_id" SET NOT NULL;
