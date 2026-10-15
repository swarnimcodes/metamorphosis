-- +goose Up
CREATE TABLE users (
       id    	   UUID	PRIMARY KEY DEFAULT uuidv7(),
       email	   TEXT	NOT NULL UNIQUE,
       first_name  TEXT	NOT NULL,
       last_name   TEXT	NOT NULL,
       username    TEXT	NOT NULL UNIQUE,
       created_at  TIMESTAMPTZ	NOT NULL DEFAULT now(),
       updated_at  TIMESTAMPTZ	NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
