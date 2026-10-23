-- +goose Up
-- Tool calls per user per minute, shared by every gateway replica, so a limit holds however
-- many replicas there are.
CREATE TABLE rate_limit_windows (
    user_id      bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    window_start timestamptz NOT NULL,
    calls        integer     NOT NULL,
    PRIMARY KEY (user_id, window_start)
);

-- +goose Down
DROP TABLE rate_limit_windows;
