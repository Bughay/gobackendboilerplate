-- Revert 000001_init in reverse dependency order.
-- Dropping a table also drops its indexes and constraints.

DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS user_profiles;
DROP TABLE IF EXISTS users;
