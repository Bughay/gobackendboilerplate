-- products.sql — every query the products module runs.
-- Each `-- name:` block becomes one typed method on queries.Queries.
--
-- Every statement is scoped by user_id so one user can never read or write
-- another user's rows, even if a handler ever forgets to check ownership.

-- name: CreateProduct :one
INSERT INTO products (user_id, name, qty, price)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, name, date_added, qty, price;

-- name: ListProducts :many
SELECT id, user_id, name, date_added, qty, price
FROM products
WHERE user_id = $1
ORDER BY date_added DESC;

-- name: GetProduct :one
SELECT id, user_id, name, date_added, qty, price
FROM products
WHERE id = $1 AND user_id = $2;

-- name: UpdateProduct :one
-- RETURNING yields no row when the id is missing or owned by someone else, which
-- the repository turns into ErrNotFound.
UPDATE products
SET name = $3, qty = $4, price = $5
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, name, date_added, qty, price;

-- name: DeleteProduct :execrows
-- :execrows returns the affected row count. Zero means the product is missing or
-- not owned by this user, which the repository maps onto ErrNotFound.
DELETE FROM products
WHERE id = $1 AND user_id = $2;
