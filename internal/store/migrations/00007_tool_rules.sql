-- +goose Up
-- Which tools members may use; admins may use every tool. A tool no rule matches is allowed,
-- and among the rules that match a call, the most specific decides (internal/gateway/policy).
CREATE TABLE tool_rules (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- The subject: every member (user_id NULL) or one user.
    user_id    bigint      REFERENCES users (id) ON DELETE CASCADE,
    -- NULL means every server, including stores provisioned later.
    server_id  bigint      REFERENCES servers (id) ON DELETE CASCADE,
    -- An upstream tool name, without the server prefix; NULL means every tool.
    tool       text        CHECK (tool <> ''),
    allow      boolean     NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tool_rules_one_per_scope UNIQUE NULLS NOT DISTINCT (user_id, server_id, tool)
);

-- Members may read a store but not change its catalog or orders.
INSERT INTO tool_rules (tool, allow) VALUES ('create_product', false), ('update_order_status', false);

-- +goose Down
DROP TABLE tool_rules;
