-- 000001_init.up.sql
-- Baseline schema: accounts, optional profiles, refresh tokens, the product
-- catalog, and customer orders (including guest orders written after payment).

-- users: one row per account. `roles` is a text array so a user can hold more
-- than one role; the admin role lives in the same column. Emails are normalized
-- (lowercased) by the application before they reach this table, and the
-- functional unique index below enforces case-insensitive uniqueness.
CREATE TABLE IF NOT EXISTS "users" (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL UNIQUE,
    roles text[] NOT NULL DEFAULT ARRAY['user'],
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- Backs the case-insensitive email lookup used by the auth repository. The
-- plain UNIQUE on email stays as defense in depth for normalized writes.
CREATE UNIQUE INDEX IF NOT EXISTS users_lower_email_idx ON "users"(lower(email));

-- user_profiles: optional one-to-one profile row. The app-specific profile
-- columns are added by whichever app needs them; the UNIQUE FK also indexes the
-- user_id join and enforces at most one profile per user.
CREATE TABLE IF NOT EXISTS "user_profiles" (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL UNIQUE REFERENCES "users"(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- refresh_tokens: the allowlist of live refresh tokens, stored as SHA-256
-- hex digests. The application consumes a row when a token is rotated.
CREATE TABLE IF NOT EXISTS "refresh_tokens" (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES "users"(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON "refresh_tokens"(user_id);

-- products: the store catalog. `user_id` is the admin who created the row.
-- Deleting that admin is restricted while they still own catalog rows, so the
-- catalog can never silently disappear with an account.
CREATE TABLE IF NOT EXISTS "products" (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES "users"(id) ON DELETE RESTRICT,
    name text NOT NULL,
    date_added timestamptz NOT NULL DEFAULT now(),
    qty integer NOT NULL,
    price numeric(12,2) NOT NULL
);
CREATE INDEX IF NOT EXISTS products_user_id_date_added_idx
    ON "products"(user_id, date_added DESC);

-- orders: one row per completed purchase. `user_id` is NULL for a guest order;
-- `email` is the contact snapshot and the one-time claim key. Orders are written
-- only after payment, so `status` starts at 'paid'. `payment_id` is the
-- provider's payment/session reference and doubles as the webhook idempotency
-- key, so a retried webhook cannot create a duplicate order.
CREATE TABLE IF NOT EXISTS "orders" (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid REFERENCES "users"(id) ON DELETE RESTRICT,
    email text NOT NULL,
    status text NOT NULL DEFAULT 'paid',
    total numeric(12,2) NOT NULL,
    payment_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS orders_user_id_created_at_idx
    ON "orders"(user_id, created_at DESC);
-- Supports the one-time claim of unclaimed guest orders by a verified account
-- email. Only NULL user_id rows are candidates; claimed orders are never
-- reassigned.
CREATE INDEX IF NOT EXISTS orders_unclaimed_email_idx
    ON "orders"(lower(email)) WHERE user_id IS NULL;

-- order_items: the products inside an order. `product_name` and `unit_price`
-- are snapshots taken at purchase time, so later catalog edits or deletes do
-- not rewrite order history. `product_id` becomes NULL if the catalog row is
-- deleted; the snapshot keeps the line readable.
CREATE TABLE IF NOT EXISTS "order_items" (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL REFERENCES "orders"(id) ON DELETE CASCADE,
    product_id uuid REFERENCES "products"(id) ON DELETE SET NULL,
    product_name text NOT NULL,
    unit_price numeric(12,2) NOT NULL,
    qty integer NOT NULL
);
CREATE INDEX IF NOT EXISTS order_items_order_id_idx ON "order_items"(order_id);
