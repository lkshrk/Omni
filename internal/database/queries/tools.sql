-- name: UpsertTool :one
INSERT INTO tool_cache (name, provider, package, installed, version, last_checked)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(name, provider, package) DO UPDATE SET
    installed    = excluded.installed,
    version      = excluded.version,
    last_checked = excluded.last_checked
RETURNING *;

-- name: GetTool :one
SELECT * FROM tool_cache WHERE name = ? AND provider = ? AND package = ? LIMIT 1;

-- name: ListTools :many
SELECT * FROM tool_cache ORDER BY provider, name;

-- name: ListToolsByProvider :many
SELECT * FROM tool_cache WHERE provider = ? ORDER BY name;

-- name: DeleteTool :exec
DELETE FROM tool_cache WHERE name = ? AND provider = ? AND package = ?;

-- name: MarkInstalled :exec
UPDATE tool_cache
SET installed = 1, version = ?, last_checked = ?
WHERE name = ? AND provider = ? AND package = ?;

-- name: MarkUninstalled :exec
UPDATE tool_cache
SET installed = 0, version = NULL, last_checked = ?
WHERE name = ? AND provider = ? AND package = ?;
