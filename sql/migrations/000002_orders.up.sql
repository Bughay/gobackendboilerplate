-- 000002_orders.up.sql
-- Orders can now be created directly from the catalog before payment. The
-- payment flow may fill payment_id later, and a new order starts at 'pending'
-- instead of the old default 'paid'.

ALTER TABLE "orders" ALTER COLUMN "payment_id" DROP NOT NULL;
ALTER TABLE "orders" ALTER COLUMN "status" SET DEFAULT 'pending';
