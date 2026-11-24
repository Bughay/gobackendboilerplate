-- orders.sql — every query the orders module runs.
--
-- Orders are owned by the authenticated user. Every read is scoped by user_id
-- so one user can never see another user's orders, and every product lookup is
-- scoped by user_id so one user can never order from another user's catalog.
--
-- The explicit ::uuid casts keep generated parameter types as plain Go strings;
-- nullable result columns are mapped to pointers by sqlc.yaml.

-- name: GetOrderProducts :many
-- Loads the catalog rows an order refers to. Restricting to user_id prevents a
-- caller from ordering another user's products by guessing an ID. FOR SHARE
-- keeps the price/name snapshot stable and blocks a concurrent delete until the
-- order transaction commits.
SELECT p.id, p.name, p.price
FROM products p
WHERE p.user_id = sqlc.arg(user_id)
  AND p.id = ANY(sqlc.arg(product_ids)::uuid[])
FOR SHARE;

-- name: CreateOrder :one
-- Inserts the order and snapshots the creator's email from the users row. The
-- status starts at 'pending' because this endpoint does not take a payment. A
-- missing user row returns no rows, which the repository maps to ErrUserNotFound.
INSERT INTO orders (user_id, email, status, total)
SELECT u.id, u.email, 'pending', sqlc.arg(total)
FROM users u
WHERE u.id = sqlc.arg(user_id)::uuid
RETURNING id, user_id, email, status, total, created_at;

-- name: CreateOrderItem :one
-- Each line snapshots the product name and unit price at purchase time, so a
-- later catalog edit or delete cannot rewrite order history.
INSERT INTO order_items (order_id, product_id, product_name, unit_price, qty)
VALUES (sqlc.arg(order_id)::uuid, sqlc.arg(product_id)::uuid,
        sqlc.arg(product_name), sqlc.arg(unit_price), sqlc.arg(qty))
RETURNING id;

-- name: ListOrdersByUser :many
-- Newest first. The id tiebreaker keeps the order stable when two orders share a
-- created_at timestamp.
SELECT o.id, o.user_id, o.email, o.status, o.total, o.created_at
FROM orders o
WHERE o.user_id = sqlc.arg(user_id)::uuid
ORDER BY o.created_at DESC, o.id DESC;

-- name: GetOrder :one
-- The user_id predicate makes "missing" and "owned by someone else" identical,
-- so ownership cannot be probed.
SELECT o.id, o.user_id, o.email, o.status, o.total, o.created_at
FROM orders o
WHERE o.id = sqlc.arg(id)::uuid AND o.user_id = sqlc.arg(user_id)::uuid;

-- name: ListOrderItems :many
-- Returns every item belonging to the user's orders in one query. The repository
-- groups these rows by order_id when building the list response.
SELECT oi.id, oi.order_id, oi.product_id, oi.product_name, oi.unit_price, oi.qty
FROM order_items oi
JOIN orders o ON o.id = oi.order_id
WHERE o.user_id = sqlc.arg(user_id)::uuid
ORDER BY oi.id;

-- name: ListOrderItemsByOrder :many
-- Items for one order. The caller has already verified that the order belongs
-- to this user via GetOrder.
SELECT oi.id, oi.product_id, oi.product_name, oi.unit_price, oi.qty
FROM order_items oi
WHERE oi.order_id = sqlc.arg(order_id)::uuid
ORDER BY oi.id;
