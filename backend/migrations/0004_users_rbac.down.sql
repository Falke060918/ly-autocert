-- 0004_users_rbac (down)
ALTER TABLE certificates DROP CONSTRAINT IF EXISTS certificates_department_id_fkey;
ALTER TABLE certificates DROP CONSTRAINT IF EXISTS certificates_owner_id_fkey;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS departments;
